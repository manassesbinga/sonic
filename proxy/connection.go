// Package proxy implements Sonic's transparent TLS proxy with MITM interception.
//
// It extracts SNI from TLS ClientHello, performs MITM termination,
// executes edge JavaScript workers, and forwards traffic to real servers.
//
// Usage:
//
//	proxy := proxy.NewTransparentProxy(cfg, mitmEngine, jsEngine)
//	proxy.Start()
//	// ... wait for signal ...
//	proxy.Stop()
package proxy

import (
	"bufio"
	"io"
	"net"
)

// BufferedConn wraps a net.Conn with a buffered reader, allowing
// byte-level inspection (Peek) without consuming data from the stream.
// Used for TLS ClientHello SNI extraction.
type BufferedConn struct {
	net.Conn
	reader io.Reader
}

// NewBufferedConn creates a buffered wrapper around a TCP connection.
func NewBufferedConn(c net.Conn) *BufferedConn {
	bufReader := bufio.NewReader(c)
	return &BufferedConn{
		Conn:   c,
		reader: bufReader,
	}
}

// Peek returns the first n bytes without consuming them.
// Returns an error if fewer than n bytes are available.
func (bc *BufferedConn) Peek(n int) ([]byte, error) {
	if bufReader, ok := bc.reader.(*bufio.Reader); ok {
		return bufReader.Peek(n)
	}
	return nil, io.ErrUnexpectedEOF
}

// Read reads from the buffered reader, consuming peeked bytes first.
func (bc *BufferedConn) Read(b []byte) (int, error) {
	return bc.reader.Read(b)
}
