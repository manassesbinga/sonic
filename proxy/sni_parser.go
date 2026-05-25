package proxy

import (
	"errors"
)

// ExtractSNI parses a raw TLS ClientHello byte payload and returns
// the Server Name Indication (SNI) hostname.
//
// It performs strict bounds checking at every step to prevent
// buffer overflow or panic on malformed input.
//
// Returns an error if:
//   - The data is not a TLS Handshake record
//   - The record is not a ClientHello
//   - Any length field exceeds available data
//   - No SNI extension is found
//
// Reference: RFC 5246 (TLS 1.2) §7.4.1.2, RFC 6066 §3
func ExtractSNI(data []byte) (string, error) {
	if len(data) < 5 {
		return "", errors.New("dados insuficientes para TLS Record")
	}

	if data[0] != 0x16 {
		return "", errors.New("nao e um record TLS Handshake")
	}

	recordLen := int(data[3])<<8 | int(data[4])
	if len(data) < recordLen+5 {
		return "", errors.New("record TLS Handshake incompleto")
	}

	pos := 5

	if data[pos] != 0x01 {
		return "", errors.New("nao e um TLS Client Hello")
	}

	pos += 4

	pos += 2 + 32

	if pos >= len(data) {
		return "", errors.New("fim inesperado de dados apos Random")
	}

	sessionIDLen := int(data[pos])
	pos += 1 + sessionIDLen

	if pos+2 > len(data) {
		return "", errors.New("fim inesperado de dados antes de Cipher Suites")
	}

	cipherSuitesLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2 + cipherSuitesLen

	if pos+1 > len(data) {
		return "", errors.New("fim inesperado de dados antes de Compression Methods")
	}

	compressionLen := int(data[pos])
	pos += 1 + compressionLen

	if pos+2 > len(data) {
		return "", errors.New("sem extensoes TLS")
	}

	extensionsLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2

	extensionsEnd := pos + extensionsLen
	if extensionsEnd > len(data) {
		extensionsEnd = len(data)
	}

	for pos+4 <= extensionsEnd {
		extType := int(data[pos])<<8 | int(data[pos+1])
		extLen := int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4

		if pos+extLen > extensionsEnd {
			return "", errors.New("tamanho de extensao invalido")
		}

		if extType == 0 {
			sniPos := pos
			if sniPos+2 > pos+extLen {
				return "", errors.New("extensao SNI malformada")
			}
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
