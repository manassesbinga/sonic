package neuralcache

import (
	"container/heap"
	"encoding/binary"
	"errors"
	"strings"
)

// ---------------------------------------------------------------------------
// Configuração de Compressão
// ---------------------------------------------------------------------------

const (
	lz77WindowSize    = 4096 // Janela deslizante de 4KB para busca de matches
	lz77MaxMatchLen   = 258  // Comprimento máximo de um match (como DEFLATE)
	lz77MinMatchLen   = 3    // Comprimento mínimo para valer a pena (match = 5 bytes, 3 literals = 6 bytes)
	compressMinSize   = 512  // Payloads menores que 512B não são comprimidos (overhead > ganho)
	maxDecompressSize = 20 * 1024 * 1024 // 20MB — limite contra zip bombs
)

// Flags do formato de compressão
const (
	flagUncompressed byte = 0x00 // Dados armazenados sem compressão
	flagCompressed   byte = 0x01 // Dados comprimidos com LZ77+Huffman
)

// ---------------------------------------------------------------------------
// Erros
// ---------------------------------------------------------------------------

var (
	ErrCorruptedData = errors.New("compression: dados corrompidos ou truncados")
	ErrInvalidHeader = errors.New("compression: header de compressão inválido")
	ErrDecompressTooLarge = errors.New("compression: dados descomprimidos excedem limite de segurança")
)

// ---------------------------------------------------------------------------
// LZ77 — Compressão por Dicionário Deslizante
// ---------------------------------------------------------------------------
//
// O LZ77 procura padrões repetidos numa janela de bytes anteriores.
// Quando encontra uma repetição, substitui por uma referência (offset, length)
// em vez de copiar os bytes novamente.
//
// Exemplo: "ABCABCABC"
//   → Literal(A), Literal(B), Literal(C), Match(offset=3, length=6)
//
// A janela deslizante de 4KB significa que podemos referenciar até
// 4096 bytes para trás no fluxo de dados.

// lz77Token representa um token na saída do LZ77.
// Pode ser um Literal (byte único) ou um Match (referência a dados anteriores).
type lz77Token struct {
	IsMatch bool   // true = Match, false = Literal
	Literal byte   // Byte literal (usado quando IsMatch == false)
	Offset  uint16 // Distância para trás no buffer (usado quando IsMatch == true)
	Length  uint16 // Comprimento do match (usado quando IsMatch == true)
}

// lz77Encode comprime os dados usando o algoritmo LZ77 com janela deslizante.
//
// Complexidade: O(n * w) onde n = tamanho dos dados, w = tamanho da janela.
// Para payloads de proxy HTTP (< 10MB), isto é eficiente o suficiente.
func lz77Encode(data []byte) []lz77Token {
	if len(data) == 0 {
		return nil
	}

	tokens := make([]lz77Token, 0, len(data)/2)
	pos := 0

	for pos < len(data) {
		bestOffset := 0
		bestLength := 0

		// Define o início da janela de busca (máximo 4KB para trás)
		windowStart := pos - lz77WindowSize
		if windowStart < 0 {
			windowStart = 0
		}

		// Define o comprimento máximo do match (limitado pelo espaço restante)
		maxLen := len(data) - pos
		if maxLen > lz77MaxMatchLen {
			maxLen = lz77MaxMatchLen
		}

		// Procura o match mais longo na janela deslizante
		if maxLen >= lz77MinMatchLen {
			for i := windowStart; i < pos; i++ {
				matchLen := 0
				// Compara byte a byte. Note que i+matchLen pode ultrapassar pos,
				// o que permite codificar repetições (run-length encoding dentro do LZ77).
				// Isto funciona porque durante a codificação, todos os dados estão disponíveis.
				for matchLen < maxLen && data[i+matchLen] == data[pos+matchLen] {
					matchLen++
				}

				if matchLen > bestLength {
					bestLength = matchLen
					bestOffset = pos - i
					// Early exit: já encontramos o match máximo possível
					if bestLength == maxLen {
						break
					}
				}
			}
		}

		if bestLength >= lz77MinMatchLen {
			tokens = append(tokens, lz77Token{
				IsMatch: true,
				Offset:  uint16(bestOffset),
				Length:   uint16(bestLength),
			})
			pos += bestLength
		} else {
			tokens = append(tokens, lz77Token{
				IsMatch: false,
				Literal: data[pos],
			})
			pos++
		}
	}

	return tokens
}

// lz77Decode reconstrói os dados originais a partir dos tokens LZ77.
//
// Para matches com overlap (offset < length), copia byte a byte,
// permitindo expansão de runs — ex: offset=1,length=5 copia o mesmo byte 5 vezes.
func lz77Decode(tokens []lz77Token) ([]byte, error) {
	output := make([]byte, 0, len(tokens)*2)

	for _, tok := range tokens {
		if tok.IsMatch {
			// Validação: o offset deve referenciar dados já descodificados
			if int(tok.Offset) > len(output) || tok.Offset == 0 {
				return nil, ErrCorruptedData
			}
			start := len(output) - int(tok.Offset)
			// Copia byte a byte para lidar com overlaps correctamente
			for i := 0; i < int(tok.Length); i++ {
				output = append(output, output[start+i])
			}
		} else {
			output = append(output, tok.Literal)
		}

		// Proteção contra zip bombs
		if len(output) > maxDecompressSize {
			return nil, ErrDecompressTooLarge
		}
	}

	return output, nil
}

// serializeTokens converte os tokens LZ77 para formato binário.
//
// Formato:
//   - Literal:  [0x00] [byte]                          = 2 bytes
//   - Match:    [0x01] [offset_hi] [offset_lo] [len_hi] [len_lo] = 5 bytes
//
// Um match de comprimento L poupa (2*L - 5) bytes vs L literais.
// Para L >= 3: poupa pelo menos 1 byte (2*3 - 5 = 1).
func serializeTokens(tokens []lz77Token) []byte {
	buf := make([]byte, 0, len(tokens)*3)

	for _, tok := range tokens {
		if tok.IsMatch {
			buf = append(buf, 0x01)
			buf = append(buf, byte(tok.Offset>>8), byte(tok.Offset))
			buf = append(buf, byte(tok.Length>>8), byte(tok.Length))
		} else {
			buf = append(buf, 0x00)
			buf = append(buf, tok.Literal)
		}
	}

	return buf
}

// deserializeTokens reconstrói os tokens LZ77 a partir do formato binário.
func deserializeTokens(data []byte) ([]lz77Token, error) {
	tokens := make([]lz77Token, 0, len(data)/2)
	i := 0

	for i < len(data) {
		switch data[i] {
		case 0x00: // Literal
			if i+1 >= len(data) {
				return nil, ErrCorruptedData
			}
			tokens = append(tokens, lz77Token{
				IsMatch: false,
				Literal: data[i+1],
			})
			i += 2

		case 0x01: // Match
			if i+4 >= len(data) {
				return nil, ErrCorruptedData
			}
			offset := uint16(data[i+1])<<8 | uint16(data[i+2])
			length := uint16(data[i+3])<<8 | uint16(data[i+4])
			tokens = append(tokens, lz77Token{
				IsMatch: true,
				Offset:  offset,
				Length:   length,
			})
			i += 5

		default:
			return nil, ErrCorruptedData
		}
	}

	return tokens, nil
}

// ---------------------------------------------------------------------------
// Huffman Coding — Compressão por Entropia
// ---------------------------------------------------------------------------
//
// O Huffman Coding atribui códigos de bits de comprimento variável a cada
// símbolo (byte) com base na sua frequência nos dados:
//   - Bytes frequentes → códigos curtos (ex: espaço ' ' → 2 bits)
//   - Bytes raros → códigos longos (ex: 0xFF → 12 bits)
//
// Isto explora a redundância estatística: se um byte aparece 50% das vezes,
// precisa apenas de 1 bit em vez de 8.

// huffmanNode representa um nó na árvore binária de Huffman.
type huffmanNode struct {
	freq  uint32       // Frequência do símbolo (ou soma dos filhos)
	sym   byte         // Símbolo (byte) — apenas significativo em folhas
	left  *huffmanNode // Filho esquerdo (bit 0)
	right *huffmanNode // Filho direito (bit 1)
	leaf  bool         // true se este nó é uma folha (símbolo real)
}

// huffmanCode representa o código de bits atribuído a um símbolo.
type huffmanCode struct {
	bits   uint32 // Os bits do código (alinhados à direita)
	length uint8  // Número de bits no código (1-32)
}

// huffmanHeap implementa um min-heap de nós Huffman, ordenado por frequência.
// Usado para construir a árvore de Huffman de baixo para cima.
type huffmanHeap []*huffmanNode

func (h huffmanHeap) Len() int            { return len(h) }
func (h huffmanHeap) Less(i, j int) bool   { return h[i].freq < h[j].freq }
func (h huffmanHeap) Swap(i, j int)        { h[i], h[j] = h[j], h[i] }
func (h *huffmanHeap) Push(x interface{})  { *h = append(*h, x.(*huffmanNode)) }
func (h *huffmanHeap) Pop() interface{} {
	old := *h
	n := len(old)
	node := old[n-1]
	old[n-1] = nil // evita memory leak
	*h = old[:n-1]
	return node
}

// buildHuffmanTree constrói a árvore de Huffman a partir de uma tabela de frequências.
//
// Algoritmo:
//   1. Cria um nó folha para cada símbolo com frequência > 0
//   2. Insere todos os nós num min-heap (ordenado por frequência)
//   3. Repete até restar apenas 1 nó:
//      a. Remove os 2 nós com menor frequência
//      b. Cria um nó pai com freq = soma das frequências
//      c. Insere o nó pai de volta no heap
//   4. O último nó é a raiz da árvore
func buildHuffmanTree(freq [256]uint32) *huffmanNode {
	h := &huffmanHeap{}
	heap.Init(h)

	for i := 0; i < 256; i++ {
		if freq[i] > 0 {
			heap.Push(h, &huffmanNode{
				freq: freq[i],
				sym:  byte(i),
				leaf: true,
			})
		}
	}

	// Sem símbolos: dados vazios
	if h.Len() == 0 {
		return nil
	}

	// Caso especial: apenas 1 símbolo único
	// Criamos um nó pai com o único símbolo como filho esquerdo.
	// O código atribuído será "0" com comprimento 1.
	if h.Len() == 1 {
		node := heap.Pop(h).(*huffmanNode)
		return &huffmanNode{
			freq: node.freq,
			left: node,
		}
	}

	// Construção bottom-up: combina os 2 nós menos frequentes iterativamente
	for h.Len() > 1 {
		left := heap.Pop(h).(*huffmanNode)
		right := heap.Pop(h).(*huffmanNode)
		parent := &huffmanNode{
			freq:  left.freq + right.freq,
			left:  left,
			right: right,
		}
		heap.Push(h, parent)
	}

	return heap.Pop(h).(*huffmanNode)
}

// generateCodes percorre a árvore de Huffman e gera os códigos de bits.
//
// Regra: ir para a esquerda = bit 0, ir para a direita = bit 1.
// Cada folha recebe o código correspondente ao caminho desde a raiz.
func generateCodes(root *huffmanNode) [256]huffmanCode {
	var codes [256]huffmanCode
	if root == nil {
		return codes
	}

	var traverse func(node *huffmanNode, bits uint32, length uint8)
	traverse = func(node *huffmanNode, bits uint32, length uint8) {
		if node.leaf {
			if length == 0 {
				// Caso especial: símbolo único → código "0" com comprimento 1
				codes[node.sym] = huffmanCode{bits: 0, length: 1}
			} else {
				codes[node.sym] = huffmanCode{bits: bits, length: length}
			}
			return
		}
		if node.left != nil {
			traverse(node.left, bits<<1, length+1)
		}
		if node.right != nil {
			traverse(node.right, (bits<<1)|1, length+1)
		}
	}

	traverse(root, 0, 0)
	return codes
}

// huffmanEncode comprime dados usando Huffman Coding.
//
// Formato de saída:
//   [OrigSize:4] [NumSymbols:2] [Sym:1 + Freq:4]×N [PaddingBits:1] [Bitstream...]
//
// O OrigSize permite ao descodificador saber exatamente quantos símbolos esperar.
// A tabela de frequências permite reconstruir a árvore no lado da descodificação.
func huffmanEncode(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}

	// 1. Conta frequências de cada byte
	var freq [256]uint32
	for _, b := range data {
		freq[b]++
	}

	// 2. Constrói a árvore e gera códigos
	tree := buildHuffmanTree(freq)
	codes := generateCodes(tree)

	// 3. Conta símbolos únicos para o header
	numSymbols := 0
	for i := 0; i < 256; i++ {
		if freq[i] > 0 {
			numSymbols++
		}
	}

	// 4. Calcula o total de bits necessários
	totalBits := uint64(0)
	for _, b := range data {
		totalBits += uint64(codes[b].length)
	}

	compressedByteCount := (totalBits + 7) / 8
	paddingBits := byte((8 - (totalBits % 8)) % 8)

	// 5. Monta o output
	headerSize := 4 + 2 + numSymbols*5 + 1 // origSize + numSymbols + freq table + padding
	output := make([]byte, 0, headerSize+int(compressedByteCount))

	// Header: tamanho original (uint32 big-endian)
	origSizeBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(origSizeBytes, uint32(len(data)))
	output = append(output, origSizeBytes...)

	// Header: tabela de frequências
	numSymBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(numSymBytes, uint16(numSymbols))
	output = append(output, numSymBytes...)

	for i := 0; i < 256; i++ {
		if freq[i] > 0 {
			output = append(output, byte(i))
			freqBytes := make([]byte, 4)
			binary.BigEndian.PutUint32(freqBytes, freq[i])
			output = append(output, freqBytes...)
		}
	}

	// Header: bits de padding
	output = append(output, paddingBits)

	// 6. Codifica o bitstream
	var currentByte byte
	var bitPos uint8

	for _, b := range data {
		code := codes[b]
		// Escreve cada bit do código, do mais significativo para o menos
		for i := code.length; i > 0; i-- {
			bit := (code.bits >> (i - 1)) & 1
			currentByte = (currentByte << 1) | byte(bit)
			bitPos++
			if bitPos == 8 {
				output = append(output, currentByte)
				currentByte = 0
				bitPos = 0
			}
		}
	}

	// Flush dos bits restantes com padding
	if bitPos > 0 {
		currentByte <<= (8 - bitPos)
		output = append(output, currentByte)
	}

	return output
}

// huffmanDecode descomprime dados codificados com Huffman.
func huffmanDecode(compressed []byte) ([]byte, error) {
	if len(compressed) == 0 {
		return nil, nil
	}

	pos := 0

	// 1. Lê o tamanho original
	if pos+4 > len(compressed) {
		return nil, ErrCorruptedData
	}
	origSize := int(binary.BigEndian.Uint32(compressed[pos : pos+4]))
	pos += 4

	if origSize > maxDecompressSize {
		return nil, ErrDecompressTooLarge
	}

	// 2. Lê a tabela de frequências
	if pos+2 > len(compressed) {
		return nil, ErrCorruptedData
	}
	numSymbols := int(binary.BigEndian.Uint16(compressed[pos : pos+2]))
	pos += 2

	if numSymbols > 256 {
		return nil, ErrCorruptedData
	}

	var freq [256]uint32
	for i := 0; i < numSymbols; i++ {
		if pos+5 > len(compressed) {
			return nil, ErrCorruptedData
		}
		sym := compressed[pos]
		f := binary.BigEndian.Uint32(compressed[pos+1 : pos+5])
		freq[sym] = f
		pos += 5
	}

	// 3. Lê os bits de padding
	if pos+1 > len(compressed) {
		return nil, ErrCorruptedData
	}
	paddingBits := compressed[pos]
	pos++

	if paddingBits > 7 {
		return nil, ErrCorruptedData
	}

	// 4. Reconstrói a árvore de Huffman
	tree := buildHuffmanTree(freq)
	if tree == nil {
		return nil, ErrCorruptedData
	}

	// 5. Descodifica o bitstream
	bitstream := compressed[pos:]
	totalBits := uint64(len(bitstream))*8 - uint64(paddingBits)

	output := make([]byte, 0, origSize)
	node := tree
	var bitIdx uint64

	for len(output) < origSize && bitIdx < totalBits {
		byteIdx := bitIdx / 8
		bitOffset := 7 - (bitIdx % 8)

		if int(byteIdx) >= len(bitstream) {
			break
		}

		bit := (bitstream[byteIdx] >> bitOffset) & 1

		if bit == 0 {
			node = node.left
		} else {
			node = node.right
		}

		if node == nil {
			return nil, ErrCorruptedData
		}

		if node.leaf {
			output = append(output, node.sym)
			node = tree // volta à raiz para o próximo símbolo
		}

		bitIdx++
	}

	if len(output) != origSize {
		return nil, ErrCorruptedData
	}

	return output, nil
}

// ---------------------------------------------------------------------------
// Pipeline Combinado: LZ77 → Huffman
// ---------------------------------------------------------------------------
//
// A combinação dos dois algoritmos é a base do formato DEFLATE (usado no gzip):
//   1. LZ77 remove redundância estrutural (strings repetidas)
//   2. Huffman remove redundância estatística (bytes frequentes)
//
// Formato final:
//   [Flag:1] [Dados...]
//   - Flag 0x00: dados não comprimidos (raw)
//   - Flag 0x01: dados comprimidos com LZ77+Huffman

// Compress comprime dados usando o pipeline LZ77 → Huffman.
//
// Se a compressão não reduzir o tamanho (dados aleatórios, binários, etc.),
// os dados são armazenados sem compressão com flag 0x00.
func Compress(data []byte) []byte {
	if len(data) == 0 {
		return []byte{flagUncompressed}
	}

	// Passo 1: LZ77 — encontra padrões repetidos
	tokens := lz77Encode(data)

	// Passo 2: Serializa os tokens LZ77 para bytes
	serialized := serializeTokens(tokens)

	// Passo 3: Huffman — comprime pela frequência dos bytes
	compressed := huffmanEncode(serialized)

	// Verificação: se a compressão não ajudou, armazena raw
	if len(compressed) >= len(data) {
		result := make([]byte, 1+len(data))
		result[0] = flagUncompressed
		copy(result[1:], data)
		return result
	}

	// Retorna com flag de comprimido
	result := make([]byte, 1+len(compressed))
	result[0] = flagCompressed
	copy(result[1:], compressed)
	return result
}

// Decompress descomprime dados previamente comprimidos com Compress.
func Decompress(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, ErrCorruptedData
	}

	flag := data[0]
	payload := data[1:]

	switch flag {
	case flagUncompressed:
		// Dados não comprimidos — retorna cópia directa
		result := make([]byte, len(payload))
		copy(result, payload)
		return result, nil

	case flagCompressed:
		// Passo 1: Huffman decode → bytes serializados dos tokens LZ77
		serialized, err := huffmanDecode(payload)
		if err != nil {
			return nil, err
		}

		// Passo 2: Deserializa os tokens LZ77
		tokens, err := deserializeTokens(serialized)
		if err != nil {
			return nil, err
		}

		// Passo 3: LZ77 decode → dados originais
		return lz77Decode(tokens)

	default:
		return nil, ErrInvalidHeader
	}
}

// ---------------------------------------------------------------------------
// Decisão Inteligente de Compressão
// ---------------------------------------------------------------------------

// ShouldCompress determina se um payload deve ser comprimido com base no
// content-type e tamanho. Tipos já comprimidos (imagens, vídeo, binários)
// são excluídos porque a compressão adicional não traria ganho.
func ShouldCompress(contentType string, size int) bool {
	if size < compressMinSize {
		return false
	}

	ct := strings.ToLower(contentType)

	// Tipos compressíveis (text-based, alta redundância)
	compressiblePrefixes := []string{
		"text/",                              // HTML, CSS, plain text, etc.
		"application/json",                   // APIs REST
		"application/xml",                    // SOAP, RSS, Atom
		"application/javascript",             // JS bundles
		"application/x-javascript",           // JS legacy
		"application/xhtml+xml",              // XHTML
		"application/soap+xml",               // SOAP
		"application/rss+xml",                // RSS
		"application/atom+xml",               // Atom feeds
		"application/svg+xml",                // SVG
		"application/x-www-form-urlencoded",  // Form data
		"application/ld+json",                // JSON-LD
		"application/graphql",                // GraphQL
		"application/wasm",                   // WebAssembly (comprime bem)
	}

	for _, prefix := range compressiblePrefixes {
		if strings.HasPrefix(ct, prefix) {
			return true
		}
	}

	return false
}
