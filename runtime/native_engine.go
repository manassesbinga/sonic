package runtime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// nativeProcess representa uma única instância persistente de um processo trabalhador nativo.
type nativeProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	reader *bufio.Reader
}

// close encerra o subprocesso e fecha os pipes associados.
func (np *nativeProcess) Close() error {
	if np.stdin != nil {
		_ = np.stdin.Close()
	}
	if np.stdout != nil {
		_ = np.stdout.Close()
	}
	if np.cmd != nil && np.cmd.Process != nil {
		_ = np.cmd.Process.Kill()
		_ = np.cmd.Wait()
	}
	return nil
}

// startProcess inicia um processo com base na extensão do arquivo.
func startProcess(filePath string) (*nativeProcess, error) {
	ext := strings.ToLower(filepath.Ext(filePath))
	var cmd *exec.Cmd

	switch ext {
	case ".py":
		cmd = exec.Command("python", filePath)
	case ".sh":
		cmd = exec.Command("bash", filePath)
	case ".rb":
		cmd = exec.Command("ruby", filePath)
	case ".pl":
		cmd = exec.Command("perl", filePath)
	case ".js":
		cmd = exec.Command("node", filePath)
	default:
		// Executa diretamente (para binários ELF ou arquivos .exe)
		cmd = exec.Command(filePath)
	}

	// Redireciona stderr para o stderr do processo pai para facilitar depuração
	cmd.Stderr = os.Stderr

	configureSysProcAttr(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("falha ao criar stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("falha ao criar stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("falha ao iniciar comando %s: %w", filePath, err)
	}

	return &nativeProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		reader: bufio.NewReader(stdout),
	}, nil
}

// communicate envia um Packet em formato JSON de linha única via stdin e aguarda o PacketResult no stdout.
func (np *nativeProcess) communicate(packet *Packet, timeout time.Duration) (*PacketResult, error) {
	data, err := json.Marshal(packet)
	if err != nil {
		return nil, fmt.Errorf("erro na serialização do pacote: %w", err)
	}

	// Envia o JSON seguido de newline (\n) como delimitador
	_, err = np.stdin.Write(append(data, '\n'))
	if err != nil {
		return nil, fmt.Errorf("erro ao escrever no stdin do subprocesso: %w", err)
	}

	type result struct {
		res *PacketResult
		err error
	}
	resChan := make(chan result, 1)

	go func() {
		for {
			line, err := np.reader.ReadString('\n')
			if err != nil {
				resChan <- result{err: fmt.Errorf("erro ao ler stdout do subprocesso (pipe fechado ou crash): %w", err)}
				return
			}

			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}

			// BLINDAGEM CONTRA RUÍDO NO STDOUT:
			// Se a linha não se parece com JSON válido (não começa com '{' e termina com '}'),
			// assumimos que é um print casual/debug de log feito pelo script do usuário.
			// Encaminhamos para a saída padrão com prefixo de debug e continuamos lendo o pipe.
			if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
				fmt.Printf("[WORKER LOG - %s] %s\n", filepath.Base(packet.Dest), trimmed)
				continue
			}

			var packetResult PacketResult
			if err := json.Unmarshal([]byte(trimmed), &packetResult); err != nil {
				resChan <- result{err: fmt.Errorf("erro na deserialização da resposta JSON do subprocesso: %w (conteúdo: %q)", err, trimmed)}
				return
			}
			resChan <- result{res: &packetResult}
			return
		}
	}()

	select {
	case r := <-resChan:
		return r.res, r.err
	case <-time.After(timeout):
		_ = np.Close()
		return nil, fmt.Errorf("timeout excedido (%v) esperando resposta do subprocesso nativo", timeout)
	}
}

// NativeEngine implementa a interface WorkerEngine para subprocessos do sistema operacional.
type NativeEngine struct {
	filePath            string
	poolSize            int
	timeoutMS           time.Duration
	processPool         chan *nativeProcess
	kvStore             *KVStore
	mu                  sync.Mutex
	quit                chan struct{}
	closed              bool
	
	// DISJUNTOR DE CIRCUITO (Circuit Breaker) para evitar bombas de spawn (Fork Bombs)
	consecutiveFailures int32
	lastFailureTime     time.Time
	disabled            bool

	activeProcesses     int32
}

// NewNativeEngine cria um novo NativeEngine com um pool de processos persistentes.
func NewNativeEngine(filePath string, timeoutMS int, poolSize int, kvStore *KVStore) (*NativeEngine, error) {
	if poolSize < 1 {
		poolSize = 4
	}

	engine := &NativeEngine{
		filePath:    filePath,
		poolSize:    poolSize,
		timeoutMS:   time.Duration(timeoutMS) * time.Millisecond,
		processPool: make(chan *nativeProcess, poolSize),
		kvStore:     kvStore,
		quit:        make(chan struct{}),
	}

	// Pré-aquece o pool de processos
	for i := 0; i < poolSize; i++ {
		np, err := startProcess(filePath)
		if err != nil {
			// Limpa processos que já foram criados
			for j := 0; j < i; j++ {
				p := <-engine.processPool
				_ = p.Close()
			}
			return nil, fmt.Errorf("falha ao inicializar o pool de processos nativos no worker %d/%d: %w", i+1, poolSize, err)
		}
		engine.processPool <- np
		engine.activeProcesses++
	}

	return engine, nil
}

// ProcessPacket implementa WorkerEngine.ProcessPacket.
func (ne *NativeEngine) ProcessPacket(packet *Packet) (*PacketResult, error) {
	np, err := ne.leaseProcess()
	if err != nil {
		return nil, fmt.Errorf("falha ao alocar processo do pool: %w", err)
	}

	// TRUNCAMENTO INTELIGENTE A 64KB CONTRA DEADLOCK DE PIPES E LATÊNCIA
	// Enviamos apenas o cabeçalho/primeiros 64KB do payload pesado para inspeção (WAF/CVE),
	// o que cabe perfeitamente no buffer do pipe do sistema operacional sem bloquear a escrita.
	var packetToCommunicate *Packet = packet
	const maxIPCPayloadBytes = 64 * 1024 // 64 KB (tamanho de buffer de pipe padrão do SO)
	
	hasLargePayload := false
	var originalData []byte
	var originalReqBody string
	var originalRespBody string

	if len(packet.Data) > maxIPCPayloadBytes {
		hasLargePayload = true
		originalData = packet.Data
	}
	if packet.Request != nil && len(packet.Request.Body) > maxIPCPayloadBytes {
		hasLargePayload = true
		originalReqBody = packet.Request.Body
	}
	if packet.Response != nil && len(packet.Response.Body) > maxIPCPayloadBytes {
		hasLargePayload = true
		originalRespBody = packet.Response.Body
	}

	if hasLargePayload {
		cloned := *packet
		if len(cloned.Data) > maxIPCPayloadBytes {
			cloned.Data = cloned.Data[:maxIPCPayloadBytes]
		}
		if cloned.Request != nil && len(cloned.Request.Body) > maxIPCPayloadBytes {
			clonedReq := *cloned.Request
			clonedReq.Body = clonedReq.Body[:maxIPCPayloadBytes]
			cloned.Request = &clonedReq
		}
		if cloned.Response != nil && len(cloned.Response.Body) > maxIPCPayloadBytes {
			clonedResp := *cloned.Response
			clonedResp.Body = clonedResp.Body[:maxIPCPayloadBytes]
			cloned.Response = &clonedResp
		}
		packetToCommunicate = &cloned
	}

	result, err := np.communicate(packetToCommunicate, ne.timeoutMS)
	if err != nil {
		// Descarta o processo com erro (fechando-o) para que um novo seja criado na próxima requisição
		ne.releaseProcess(np, false)
		ne.recordFailure()
		return nil, err
	}

	ne.recordSuccess()
	ne.releaseProcess(np, true)

	// RESTAURAÇÃO DE DADOS PESADOS NO GO:
	// Devolvemos o restante do fluxo original intacto para o motor de rede processar,
	// prevenindo perda de integridade de uploads e downloads de arquivos pesados
	if result != nil && result.Packet != nil && hasLargePayload {
		if result.Packet.Request != nil && len(originalReqBody) > 0 {
			var sentBody string
			if packetToCommunicate.Request != nil {
				sentBody = packetToCommunicate.Request.Body
			}
			if result.Packet.Request.Body == sentBody {
				result.Packet.Request.Body = originalReqBody
			}
		}
		if result.Packet.Response != nil && len(originalRespBody) > 0 {
			var sentBody string
			if packetToCommunicate.Response != nil {
				sentBody = packetToCommunicate.Response.Body
			}
			if result.Packet.Response.Body == sentBody {
				result.Packet.Response.Body = originalRespBody
			}
		}
		if len(originalData) > 0 {
			if bytes.Equal(result.Packet.Data, packetToCommunicate.Data) {
				result.Packet.Data = originalData
			}
		}
	}

	return result, nil
}

// Type implementa WorkerEngine.Type.
func (ne *NativeEngine) Type() WorkerType {
	return WorkerTypeNative
}

// Close implementa WorkerEngine.Close.
func (ne *NativeEngine) Close() error {
	ne.mu.Lock()
	if ne.closed {
		ne.mu.Unlock()
		return nil
	}
	ne.closed = true
	close(ne.quit)
	ne.mu.Unlock()

	var errs []error
drainLoop:
	for {
		select {
		case np := <-ne.processPool:
			if err := np.Close(); err != nil {
				errs = append(errs, err)
			}
			atomic.AddInt32(&ne.activeProcesses, -1)
		default:
			break drainLoop
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("erros ao fechar processos nativos do pool: %v", errs)
	}
	return nil
}

func (ne *NativeEngine) spawnProcessOnDemand() (*nativeProcess, error) {
	limit := int32(ne.poolSize * 2)
	for {
		ne.mu.Lock()
		isClosed := ne.closed
		ne.mu.Unlock()
		if isClosed {
			return nil, errors.New("NativeEngine fechada")
		}

		currentActive := atomic.LoadInt32(&ne.activeProcesses)
		if currentActive >= limit {
			// Atingiu o limite superior rígido. Aguarda por um processo disponível no pool ou timeout.
			select {
			case np := <-ne.processPool:
				ne.mu.Lock()
				isClosed = ne.closed
				ne.mu.Unlock()
				if isClosed {
					_ = np.Close()
					return nil, errors.New("NativeEngine fechada")
				}
				if np.cmd.ProcessState != nil && np.cmd.ProcessState.Exited() {
					_ = np.Close()
					atomic.AddInt32(&ne.activeProcesses, -1)
					continue
				}
				return np, nil
			case <-ne.quit:
				return nil, errors.New("NativeEngine fechada")
			case <-time.After(ne.timeoutMS):
				return nil, fmt.Errorf("NativeEngine pool exhausted: reached process limit (%d) and timeout waiting for available subprocess", limit)
			}
		}

		// Tenta incrementar atomicamente
		if atomic.CompareAndSwapInt32(&ne.activeProcesses, currentActive, currentActive+1) {
			np, err := startProcess(ne.filePath)
			if err != nil {
				// Reverte o incremento se falhar ao iniciar
				atomic.AddInt32(&ne.activeProcesses, -1)
				return nil, err
			}
			return np, nil
		}
	}
}

func (ne *NativeEngine) leaseProcess() (*nativeProcess, error) {
	ne.mu.Lock()
	if ne.closed {
		ne.mu.Unlock()
		return nil, errors.New("NativeEngine fechada")
	}

	// BLINDAGEM: Verifica se o Disjuntor de Circuito está ativo
	if ne.disabled {
		if time.Since(ne.lastFailureTime) > 30*time.Second {
			// Período de refrigeração passou, tenta reativar a engine
			ne.disabled = false
			atomic.StoreInt32(&ne.consecutiveFailures, 0)
			fmt.Printf("[INFO] NativeEngine em %s foi reativada após período de refrigeração.\n", filepath.Base(ne.filePath))
		} else {
			ne.mu.Unlock()
			return nil, fmt.Errorf("NativeEngine desativada temporariamente devido a falhas consecutivas de execução no script %s", filepath.Base(ne.filePath))
		}
	}
	ne.mu.Unlock()

	select {
	case np := <-ne.processPool:
		ne.mu.Lock()
		isClosed := ne.closed
		ne.mu.Unlock()
		if isClosed {
			_ = np.Close()
			return nil, errors.New("NativeEngine fechada")
		}
		if np.cmd.ProcessState != nil && np.cmd.ProcessState.Exited() {
			_ = np.Close()
			atomic.AddInt32(&ne.activeProcesses, -1)
			return ne.spawnProcessOnDemand()
		}
		return np, nil
	case <-ne.quit:
		return nil, errors.New("NativeEngine fechada")
	default:
		ne.mu.Lock()
		isClosed := ne.closed
		ne.mu.Unlock()
		if isClosed {
			return nil, errors.New("NativeEngine fechada")
		}
		return ne.spawnProcessOnDemand()
	}
}

func (ne *NativeEngine) releaseProcess(np *nativeProcess, ok bool) {
	ne.mu.Lock()
	shouldClose := false

	if ne.closed || !ok {
		shouldClose = true
		atomic.AddInt32(&ne.activeProcesses, -1)
	} else {
		select {
		case ne.processPool <- np:
		default:
			// Pool cheio, descarta o processo extra excedente
			shouldClose = true
			atomic.AddInt32(&ne.activeProcesses, -1)
		}
	}
	ne.mu.Unlock()

	if shouldClose {
		_ = np.Close()
	}
}

func (ne *NativeEngine) recordFailure() {
	failures := atomic.AddInt32(&ne.consecutiveFailures, 1)
	ne.mu.Lock()
	ne.lastFailureTime = time.Now()
	// Se houver 5 falhas consecutivas em subprocessos, o disjuntor desarma!
	if failures >= 5 && !ne.disabled {
		ne.disabled = true
		fmt.Printf("[CRITICAL WARNING] Circuit Breaker ativado para %s. Engine desativada por 30s devido a %d falhas consecutivas de execução.\n", filepath.Base(ne.filePath), failures)
	}
	ne.mu.Unlock()
}

func (ne *NativeEngine) recordSuccess() {
	atomic.StoreInt32(&ne.consecutiveFailures, 0)
}
