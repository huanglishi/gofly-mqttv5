package servicev5

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/huanglishi/gofly-mqttv5/corev5/messagev5"
	"github.com/huanglishi/gofly-mqttv5/logger"
	"github.com/panjf2000/ants/v2"
)

var (
	taskGPool     *ants.Pool
	taskGPoolSize = 1000
)

func InitServiceTaskPool(poolSize int) (close io.Closer) {
	if poolSize < 100 {
		poolSize = 100
	}
	taskGPool, _ = ants.NewPool(poolSize, ants.WithPanicHandler(func(i interface{}) {
		logger.Logger.Errorf("协程池处理错误：%v", i)
	}), ants.WithMaxBlockingTasks(poolSize*2))
	taskGPoolSize = poolSize
	return &closer{}
}

type closer struct {
}

func (closer closer) Close() error {
	taskGPool.Release()
	return nil
}
func submit(f func()) {
	dealAntsErr(taskGPool.Submit(f))
}
func dealAntsErr(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, ants.ErrPoolClosed) {
		logger.Logger.Errorf("协程池错误：%v", err.Error())
		taskGPool.Reboot()
	}
	if errors.Is(err, ants.ErrPoolOverload) {
		logger.Logger.Errorf("协程池超载：%v", err.Error())
		taskGPool.Tune(int(float64(taskGPoolSize) * 1.25))
	}
	logger.Logger.Errorf("线程池处理异常：%v", err)
}

func getConnectMessage(conn io.Closer) (*messagev5.ConnectMessage, error) {
	buf, err := getMessageBuffer(conn)
	if err != nil {
		//glog.Logger.Debug("Receive error: %v", err)
		return nil, err
	}

	msg := messagev5.NewConnectMessage()

	_, err = msg.Decode(buf)
	logger.Logger.Debugf("Received: %s", msg)
	return msg, err
}

// 获取增强认证数据，或者connack数据
func getAuthMessageOrOther(conn io.Closer) (messagev5.Message, error) {
	buf, err := getMessageBuffer(conn)
	if err != nil {
		//glog.Logger.Debug("Receive error: %v", err)
		return nil, err
	}
	mtypeflags := buf[0]
	tp := messagev5.MessageType(mtypeflags >> 4)
	switch tp {
	case messagev5.DISCONNECT:
		dis := messagev5.NewDisconnectMessage()
		_, err = dis.Decode(buf)
		logger.Logger.Debugf("Received: %s", dis)
		return dis, nil
	case messagev5.AUTH:
		msg := messagev5.NewAuthMessage()
		_, err = msg.Decode(buf)
		logger.Logger.Debugf("Received: %s", msg)
		return msg, err
	case messagev5.CONNACK:
		msg := messagev5.NewConnackMessage()
		_, err = msg.Decode(buf)
		logger.Logger.Debugf("Received: %s", msg)
		return msg, err
	default:
		erMsg, er := tp.New()
		if er != nil {
			return nil, er
		}
		_, err = erMsg.Decode(buf)
		logger.Logger.Debugf("Received: %s", erMsg)
		return nil, errors.New(fmt.Sprintf("error type %v,  %v", tp.Name(), err))
	}
}
func getConnackMessage(conn io.Closer) (*messagev5.ConnackMessage, error) {
	buf, err := getMessageBuffer(conn)
	if err != nil {
		//glog.Logger.Debug("Receive error: %v", err)
		return nil, err
	}

	msg := messagev5.NewConnackMessage()

	_, err = msg.Decode(buf)
	logger.Logger.Debugf("Received: %s", msg)
	return msg, err
}

// 消息发送
func writeMessage(conn io.Closer, msg messagev5.Message) error {
	buf := make([]byte, msg.Len())
	_, err := msg.Encode(buf)
	if err != nil {
		logger.Logger.Debugf("Write error: %v", err)
		return err
	}
	logger.Logger.Debugf("Writing: %s", msg)

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
		// the messagev5 buffer
		buf []byte

		// tmp buffer to read a single byte
		b []byte = make([]byte, 1)

		// total bytes read
		l int = 0
	)

	// Let's read enough bytes to get the messagev5 header (msg type, remaining length)
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

	// Get the remaining length of the messagev5
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
