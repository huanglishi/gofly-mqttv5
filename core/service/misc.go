package service

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"

	"github.com/huanglishi/gofly-mqttv5/core/message"
	logger2 "github.com/huanglishi/gofly-mqttv5/logger"
)

func getConnectMessage(conn io.Closer) (*message.ConnectMessage, error) {
	buf, err := getMessageBuffer(conn)
	if err != nil {
		//glog.Logger.Debug("Receive error: %v", err)
		return nil, err
	}

	msg := message.NewConnectMessage()

	_, err = msg.Decode(buf)
	logger2.Logger.Debugf("Received: %s", msg)
	return msg, err
}

func getConnackMessage(conn io.Closer) (*message.ConnackMessage, error) {
	buf, err := getMessageBuffer(conn)
	if err != nil {
		//glog.Logger.Debug("Receive error: %v", err)
		return nil, err
	}

	msg := message.NewConnackMessage()

	_, err = msg.Decode(buf)
	logger2.Logger.Debugf("Received: %s", msg)
	return msg, err
}

// 消息发送
func writeMessage(conn io.Closer, msg message.Message) error {
	buf := make([]byte, msg.Len())
	_, err := msg.Encode(buf)
	if err != nil {
		logger2.Logger.Debugf("Write error: %v", err)
		return err
	}
	logger2.Logger.Debugf("Writing: %s", msg)

	return writeMessageBuffer(conn, buf)
}

func getMessageBuffer(c io.Closer) ([]byte, error) {
	if c == nil {
		return nil, ErrInvalidConnectionType
	}

	conn, ok := c.(net.Conn)
	if !ok {
		return nil, ErrInvalidConnectionType
	}

	var (
		// the message buffer
		buf []byte

		// tmp buffer to read a single byte
		b []byte = make([]byte, 1)

		// total bytes read
		l int = 0
	)

	// Let's read enough bytes to get the message header (msg type, remaining length)
	for {
		// If we have read 5 bytes and still not done, then there's a problem.
		if l > 5 {
			return nil, fmt.Errorf("connect/getMessage: 4th byte of remaining length has continuation bit set")
		}

		n, err := conn.Read(b[0:])
		if err != nil {
			//glog.Logger.Debug("Read error: %v", err)
			return nil, err
		}

		// Technically i don't think we will ever get here
		if n == 0 {
			continue
		}

		buf = append(buf, b...)
		l += n

		// Check the remlen byte (1+) to see if the continuation bit is set. If so,
		// increment cnt and continue reading. Otherwise break.
		if l > 1 && b[0] < 0x80 {
			break
		}
	}

	// Get the remaining length of the message
	remlen, _ := binary.Uvarint(buf[1:])
	buf = append(buf, make([]byte, remlen)...)

	for l < len(buf) {
		n, err := conn.Read(buf[l:])
		if err != nil {
			return nil, err
		}
		l += n
	}

	return buf, nil
}

func writeMessageBuffer(c io.Closer, b []byte) error {
	if c == nil {
		return ErrInvalidConnectionType
	}

	conn, ok := c.(net.Conn)
	if !ok {
		return ErrInvalidConnectionType
	}

	_, err := conn.Write(b)
	return err
}

// Copied from http://golang.org/src/pkg/net/timeout_test.go
func isTimeout(err error) bool {
	e, ok := err.(net.Error)
	return ok && e.Timeout()
}
