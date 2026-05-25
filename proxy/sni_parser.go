package proxy

import (
	"errors"
)

// ExtractSNI extrai o Server Name Indication (SNI) de um payload cru de TLS Client Hello.
// Nota: Projetado com verificacoes rigorosas de limites de memoria para evitar panics.
func ExtractSNI(data []byte) (string, error) {
	if len(data) < 5 {
		return "", errors.New("dados insuficientes para TLS Record")
	}

	// 1. Verifica se e um Handshake (0x16)
	if data[0] != 0x16 {
		return "", errors.New("nao e um record TLS Handshake")
	}

	recordLen := int(data[3])<<8 | int(data[4])
	if len(data) < recordLen+5 {
		return "", errors.New("record TLS Handshake incompleto")
	}

	pos := 5

	// 2. Verifica se e Client Hello (0x01)
	if data[pos] != 0x01 {
		return "", errors.New("nao e um TLS Client Hello")
	}

	// Pula Handshake Header (Type: 1 byte, Length: 3 bytes)
	pos += 4

	// Pula Protocol Version (2 bytes) e Random (32 bytes)
	pos += 2 + 32

	if pos >= len(data) {
		return "", errors.New("fim inesperado de dados apos Random")
	}

	// Session ID (1 byte length + data)
	sessionIDLen := int(data[pos])
	pos += 1 + sessionIDLen

	if pos+2 > len(data) {
		return "", errors.New("fim inesperado de dados antes de Cipher Suites")
	}

	// Cipher Suites (2 bytes length + data)
	cipherSuitesLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2 + cipherSuitesLen

	if pos+1 > len(data) {
		return "", errors.New("fim inesperado de dados antes de Compression Methods")
	}

	// Compression Methods (1 byte length + data)
	compressionLen := int(data[pos])
	pos += 1 + compressionLen

	if pos+2 > len(data) {
		return "", errors.New("sem extensoes TLS")
	}

	// Extensions (2 bytes total length + data)
	extensionsLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2

	extensionsEnd := pos + extensionsLen
	if extensionsEnd > len(data) {
		extensionsEnd = len(data)
	}

	// Varre as extensoes ate encontrar o SNI (tipo 0x0000)
	for pos+4 <= extensionsEnd {
		extType := int(data[pos])<<8 | int(data[pos+1])
		extLen := int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4

		if pos+extLen > extensionsEnd {
			return "", errors.New("tamanho de extensao invalido")
		}

		// Encontrou a extensao SNI (0x0000)
		if extType == 0 {
			sniPos := pos
			if sniPos+2 > pos+extLen {
				return "", errors.New("extensao SNI malformada")
			}
			// Server Name List Length (2 bytes)
			listLen := int(data[sniPos])<<8 | int(data[sniPos+1])
			sniPos += 2

			if sniPos+listLen > pos+extLen {
				return "", errors.New("tamanho de lista SNI invalido")
			}

			for sniPos+3 <= pos+extLen {
				nameType := data[sniPos]
				nameLen := int(data[sniPos+1])<<8 | int(data[sniPos+2])
				sniPos += 3

				if sniPos+nameLen > pos+extLen {
					return "", errors.New("tamanho de hostname SNI invalido")
				}

				// Name Type 0 indica HostName
				if nameType == 0 {
					return string(data[sniPos : sniPos+nameLen]), nil
				}
				sniPos += nameLen
			}
		}

		pos += extLen
	}

	return "", errors.New("extensao SNI nao encontrada no Client Hello")
}
