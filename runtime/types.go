package runtime

// Request representa a requisicao HTTP interceptada exposta para a VM JavaScript.
// Nota: Os campos sao exportados para permitir leitura e escrita direta a partir do JS.
type Request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Response representa a resposta HTTP interceptada exposta para a VM JavaScript.
// Nota: Os campos sao exportados para permitir leitura e escrita direta a partir do JS.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// TrafficResult representa o resultado do processamento da funcao onTraffic.
// Pode representar ou um Request modificado ou um Response de interceptacao direta (WAF).
type TrafficResult struct {
	IsResponse bool              `json:"_isResponse"`
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	Path       string            `json:"path"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
}

