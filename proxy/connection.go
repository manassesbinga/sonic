package proxy

import (
	"bufio"
	"io"
	"net"
)

// BufferedConn envolve uma conexao net.Conn para permitir espiar bytes iniciais
// e garantir que as leituras subsequentes leiam esses bytes do buffer primeiro.
type BufferedConn struct {
	net.Conn
	reader io.Reader
}

// NewBufferedConn cria um wrapper sobre uma conexao TCP para permitir o Peek.
func NewBufferedConn(c net.Conn) *BufferedConn {
	bufReader := bufio.NewReader(c)
	return &BufferedConn{
		Conn:   c,
		reader: bufReader,
	}
}

// Peek permite espreitar N bytes sem consumi-los do fluxo do socket.
func (bc *BufferedConn) Peek(n int) ([]byte, error) {
	if bufReader, ok := bc.reader.(*bufio.Reader); ok {
		return bufReader.Peek(n)
	}
	return nil, io.ErrUnexpectedEOF
}

// Read le dados do buffer primeiro, e depois diretamente do socket real.
func (bc *BufferedConn) Read(b []byte) (int, error) {
	return bc.reader.Read(b)
}
