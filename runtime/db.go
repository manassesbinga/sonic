package runtime

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Database é o gerenciador do banco de dados SQLite local do Sonic.
type Database struct {
	mu sync.RWMutex
	db *sql.DB
}

// Lock bloqueia o banco para escrita.
func (d *Database) Lock() {
	d.mu.Lock()
}

// Unlock desbloqueia o banco para escrita.
func (d *Database) Unlock() {
	d.mu.Unlock()
}

// RLock bloqueia o banco para leitura.
func (d *Database) RLock() {
	d.mu.RLock()
}

// RUnlock desbloqueia o banco para leitura.
func (d *Database) RUnlock() {
	d.mu.RUnlock()
}

// DB retorna a conexão activa do banco sql.DB.
func (d *Database) DB() *sql.DB {
	return d.db
}

var (
	globalDB       *Database
	dbMu           sync.RWMutex
	dbShutdownChan chan struct{}
	auditChan      chan auditLogRecord
	auditWg        sync.WaitGroup // Espera a goroutine do audit logger terminar o flush antes de fechar o DB
)

// InitDB inicializa o banco SQLite global na pasta configurada.
func InitDB(dataDir string) error {
	dbMu.Lock()
	defer dbMu.Unlock()

	if globalDB != nil {
		return nil
	}

	dir := dataDir
	if dir == "" {
		dir = filepath.Join(".", "data")
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create database directory: %w", err)
	}

	dbPath := filepath.Join(dir, "sonic.db")
	dsn := fmt.Sprintf("%s?_busy_timeout=5000", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("failed to open sqlite database: %w", err)
	}

	// Otimizações de performance cruciais para proxy de alta vazão
	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, _ = db.Exec("PRAGMA synchronous=NORMAL;")
	_, _ = db.Exec("PRAGMA temp_store=MEMORY;")

	gdb := &Database{db: db}
	if err := gdb.createTables(); err != nil {
		db.Close()
		return fmt.Errorf("failed to create sqlite tables: %w", err)
	}

	globalDB = gdb
	
	auditChan = make(chan auditLogRecord, 10000)
	dbShutdownChan = make(chan struct{})
	// Inicializa os loops em segundo plano
	startAuditLogger(dbShutdownChan)
	startCleanupLoop(dbShutdownChan)

	return nil
}

// GetDB retorna a instância activa do banco de dados SQLite.
func GetDB() *Database {
	dbMu.RLock()
	defer dbMu.RUnlock()
	return globalDB
}

// CloseDB encerra a conexão do banco de dados.
// Espera que a goroutine do audit logger termine o flush final antes de fechar o DB.
func CloseDB() error {
	dbMu.Lock()
	if globalDB == nil {
		dbMu.Unlock()
		return nil
	}

	// 1. Sinaliza shutdown para as goroutines de background
	if dbShutdownChan != nil {
		close(dbShutdownChan)
		dbShutdownChan = nil
	}
	auditChan = nil
	dbMu.Unlock()

	// 2. Espera que o audit logger termine o flush final (fora do lock para evitar deadlock)
	auditWg.Wait()

	// 3. Agora sim, fecha o banco de dados de forma segura
	dbMu.Lock()
	defer dbMu.Unlock()
	if globalDB != nil {
		err := globalDB.db.Close()
		globalDB = nil
		return err
	}
	return nil
}

// createTables cria os esquemas relacionais e a tabela virtual FTS5.
func (d *Database) createTables() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 1. Tabela de Rate Limits
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS rate_limits (
			ip TEXT,
			pipeline_id TEXT,
			count INTEGER,
			expires_at INTEGER,
			PRIMARY KEY (ip, pipeline_id)
		);
	`)
	if err != nil {
		return err
	}

	// 2. Tabela de Logs de Auditoria
	_, err = d.db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_logs (
			id TEXT PRIMARY KEY,
			timestamp TEXT,
			worker TEXT,
			function TEXT,
			duration TEXT,
			status TEXT,
			error_msg TEXT,
			method TEXT,
			url TEXT,
			headers TEXT,
			body TEXT,
			resp_status INTEGER,
			resp_headers TEXT,
			resp_body TEXT,
			logs TEXT
		);
		CREATE INDEX IF NOT EXISTS idx_audit_timestamp ON audit_logs(timestamp);
	`)
	if err != nil {
		return err
	}

	// 3. Tabela de Cache Semântico de IA
	_, err = d.db.Exec(`
		CREATE TABLE IF NOT EXISTS ai_cache (
			id TEXT PRIMARY KEY,
			prompt TEXT,
			response TEXT,
			embedding BLOB,
			created TEXT,
			tokens INTEGER,
			expires_at TEXT
		);
	`)
	if err != nil {
		return err
	}

	// 4. Tabela Virtual FTS5 para busca rápida e indexação
	_, err = d.db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS ai_cache_fts USING fts5(
			prompt,
			response,
			tokenize='porter'
		);
	`)
	return err
}

// AUDIT LOG SYSTEM (Gravação assíncrona e concorrente)

type auditLogRecord struct {
	ID          string
	Timestamp   time.Time
	Worker      string
	Function    string
	Duration    string
	Status      string
	ErrorMsg    string
	Method      string
	URL         string
	Headers     string
	Body        string
	RespStatus  int
	RespHeaders string
	RespBody    string
	Logs        string
}

// O canal auditChan agora é gerenciado dinamicamente no ciclo de vida de InitDB e CloseDB

func startAuditLogger(shutdownChan chan struct{}) {
	auditWg.Add(1)
	go func() {
		defer auditWg.Done()
		var batch []auditLogRecord
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		flush := func() {
			if len(batch) == 0 {
				return
			}
			_ = persistAuditLogBatch(batch)
			batch = batch[:0]
		}

		for {
			select {
			case record, ok := <-auditChan:
				if !ok {
					flush()
					return
				}
				batch = append(batch, record)
				if len(batch) >= 100 {
					flush()
				}
			case <-ticker.C:
				flush()
			case <-shutdownChan:
				flush()
				return
			}
		}
	}()
}

func isSensitiveKey(k string) bool {
	if k == "authorization" || k == "cookie" || k == "set-cookie" || k == "proxy-authorization" || k == "token" || k == "secret" || k == "key" {
		return true
	}
	return strings.HasSuffix(k, "password") ||
		strings.HasSuffix(k, "passwd") ||
		strings.HasSuffix(k, "token") ||
		strings.HasSuffix(k, "secret") ||
		strings.HasSuffix(k, "key") ||
		strings.Contains(k, "api_key") ||
		strings.Contains(k, "apikey") ||
		strings.Contains(k, "client_secret") ||
		strings.Contains(k, "access_token")
}

func redactHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	redacted := make(map[string]string)
	for k, v := range headers {
		lowerK := strings.ToLower(k)
		if isSensitiveKey(lowerK) {
			redacted[k] = "[REDACTED]"
		} else {
			redacted[k] = v
		}
	}
	return redacted
}

func redactURL(rawURL string) string {
	idx := strings.Index(rawURL, "?")
	if idx < 0 {
		return rawURL
	}
	base := rawURL[:idx]
	query := rawURL[idx+1:]
	parts := strings.Split(query, "&")
	for i, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			unescapedK, errDec := url.QueryUnescape(kv[0])
			if errDec != nil {
				unescapedK = kv[0]
			}
			k := strings.ToLower(unescapedK)
			if isSensitiveKey(k) {
				parts[i] = kv[0] + "=[REDACTED]"
			}
		}
	}
	return base + "?" + strings.Join(parts, "&")
}

func redactAny(data interface{}) {
	if m, ok := data.(map[string]interface{}); ok {
		for k, v := range m {
			lowerK := strings.ToLower(k)
			if isSensitiveKey(lowerK) {
				m[k] = "[REDACTED]"
			} else {
				redactAny(v)
			}
		}
	} else if l, ok := data.([]interface{}); ok {
		for _, item := range l {
			redactAny(item)
		}
	}
}

func redactJSONOrForm(body string) string {
	if body == "" {
		return ""
	}

	// Tenta fazer parse de JSON (suporta objetos e arrays)
	var data interface{}
	if err := json.Unmarshal([]byte(body), &data); err == nil {
		redactAny(data)
		redactedBytes, errMarshal := json.Marshal(data)
		if errMarshal == nil {
			return string(redactedBytes)
		}
	}

	// Tenta fazer parse de form urlencoded
	if strings.Contains(body, "=") && !strings.Contains(body, "{") {
		parts := strings.Split(body, "&")
		modified := false
		for i, part := range parts {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) == 2 {
				unescapedK, errDec := url.QueryUnescape(kv[0])
				if errDec != nil {
					unescapedK = kv[0]
				}
				k := strings.ToLower(unescapedK)
				if isSensitiveKey(k) {
					parts[i] = kv[0] + "=[REDACTED]"
					modified = true
				}
			}
		}
		if modified {
			return strings.Join(parts, "&")
		}
	}

	return body
}

// QueueAuditLog enfileira um log de execução para persistência assíncrona.
func QueueAuditLog(id string, timestamp time.Time, worker, function, duration, status, errorMsg, method, url string, headers map[string]string, body string, respStatus int, respHeaders map[string]string, respBody string, logs interface{}) {
	redactedHeaders := redactHeaders(headers)
	redactedRespHeaders := redactHeaders(respHeaders)
	redactedURL := redactURL(url)
	redactedBody := redactJSONOrForm(body)
	redactedRespBody := redactJSONOrForm(respBody)

	headersJSON, _ := json.Marshal(redactedHeaders)
	respHeadersJSON, _ := json.Marshal(redactedRespHeaders)
	logsJSON, _ := json.Marshal(logs)

	record := auditLogRecord{
		ID:          id,
		Timestamp:   timestamp,
		Worker:      worker,
		Function:    function,
		Duration:    duration,
		Status:      status,
		ErrorMsg:    errorMsg,
		Method:      method,
		URL:         redactedURL,
		Headers:     string(headersJSON),
		Body:        redactedBody,
		RespStatus:  respStatus,
		RespHeaders: string(respHeadersJSON),
		RespBody:    redactedRespBody,
		Logs:        string(logsJSON),
	}

	dbMu.RLock()
	ch := auditChan
	shutdown := dbShutdownChan
	dbMu.RUnlock()

	if ch == nil || shutdown == nil {
		return
	}

	select {
	case ch <- record:
	case <-shutdown:
		// Descarte se estiver fechando
	default:
		// Descarte silencioso em caso de estouro severo do buffer de auditoria (para não comprometer a vazão do proxy)
	}
}

func persistAuditLogBatch(records []auditLogRecord) error {
	gdb := GetDB()
	if gdb == nil {
		return fmt.Errorf("database not initialized")
	}

	gdb.mu.Lock()
	defer gdb.mu.Unlock()

	tx, err := gdb.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO audit_logs (
			id, timestamp, worker, function, duration, status, error_msg,
			method, url, headers, body, resp_status, resp_headers, resp_body, logs
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range records {
		_, err = stmt.Exec(r.ID, r.Timestamp.Format(time.RFC3339), r.Worker, r.Function, r.Duration, r.Status, r.ErrorMsg,
			r.Method, r.URL, r.Headers, r.Body, r.RespStatus, r.RespHeaders, r.RespBody, r.Logs)
		if err != nil {
			// Continua o lote mesmo que uma inserção individual falhe
		}
	}

	return tx.Commit()
}

func persistAuditLog(r auditLogRecord) error {
	return persistAuditLogBatch([]auditLogRecord{r})
}

// GetAuditLogs recupera as execuções salvas no banco.
func (d *Database) GetAuditLogs(limit int) ([]map[string]interface{}, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	rows, err := d.db.Query(`
		SELECT id, timestamp, worker, function, duration, status, error_msg,
		       method, url, headers, body, resp_status, resp_headers, resp_body, logs
		FROM audit_logs
		ORDER BY timestamp DESC
		LIMIT ?;
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]interface{}
	for rows.Next() {
		var id, timestamp, worker, function, duration, status, errorMsg, method, url, headers, body, respHeaders, respBody, logs string
		var respStatus int
		err := rows.Scan(&id, &timestamp, &worker, &function, &duration, &status, &errorMsg,
			&method, &url, &headers, &body, &respStatus, &respHeaders, &respBody, &logs)
		if err != nil {
			continue
		}

		var headersMap map[string]string
		var respHeadersMap map[string]string
		var logsList interface{}

		_ = json.Unmarshal([]byte(headers), &headersMap)
		_ = json.Unmarshal([]byte(respHeaders), &respHeadersMap)
		_ = json.Unmarshal([]byte(logs), &logsList)

		item := map[string]interface{}{
			"id":          id,
			"timestamp":   timestamp,
			"worker":      worker,
			"function":    function,
			"duration":    duration,
			"status":      status,
			"errorMsg":    errorMsg,
			"method":      method,
			"url":         url,
			"headers":     headersMap,
			"body":        body,
			"respStatus":  respStatus,
			"respHeaders": respHeadersMap,
			"respBody":    respBody,
			"logs":        logsList,
		}
		result = append(result, item)
	}
	return result, nil
}

// CLEANUP LOOP (Evita estouro de disco limitando tabelas)

func startCleanupLoop(shutdownChan chan struct{}) {
	ticker := time.NewTicker(30 * time.Minute)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				db := GetDB()
				if db != nil {
					db.PruneAuditLogs(5000)
					db.PruneExpiredRateLimits()
					db.PruneExpiredAICache()
				}
				// Expurgar limites inativos da memória RAM ativa
				CleanupExpiredMemoryRateLimits()
			case <-shutdownChan:
				return
			}
		}
	}()
}

// PruneAuditLogs limpa logs antigos para manter no máximo os N mais recentes.
func (d *Database) PruneAuditLogs(keepLimit int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	_, _ = d.db.Exec(`
		DELETE FROM audit_logs
		WHERE timestamp < (
			SELECT timestamp FROM audit_logs
			ORDER BY timestamp DESC
			LIMIT 1 OFFSET ?
		);
	`, keepLimit)
}

// PruneExpiredRateLimits limpa rate limits obsoletos.
func (d *Database) PruneExpiredRateLimits() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UnixNano()
	_, _ = d.db.Exec("DELETE FROM rate_limits WHERE expires_at < ?;", now)
}

// PruneExpiredAICache limpa caches com TTL vencido.
func (d *Database) PruneExpiredAICache() {
	d.mu.Lock()
	defer d.mu.Unlock()
	
	nowStr := time.Now().Format(time.RFC3339)
	
	tx, err := d.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	// 1. Remove do FTS5 em lote usando subquery
	_, _ = tx.Exec("DELETE FROM ai_cache_fts WHERE rowid IN (SELECT rowid FROM ai_cache WHERE expires_at IS NOT NULL AND expires_at < ?);", nowStr)

	// 2. Remove do cache físico
	_, _ = tx.Exec("DELETE FROM ai_cache WHERE expires_at IS NOT NULL AND expires_at < ?;", nowStr)

	_ = tx.Commit()
}


