package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubscribeMessageFields(t *testing.T) {
	msg := NewSubscribeMessage()

	msg.SetPacketId(100)
	require.Equal(t, 100, int(msg.PacketId()), "Error setting packet ID.")

	msg.AddTopic([]byte("/a/b/#/c"), 1)
	require.Equal(t, 1, len(msg.Topics()), "Error adding topic.")

	require.False(t, msg.TopicExists([]byte("a/b")), "Topic should not exist.")

	msg.RemoveTopic([]byte("/a/b/#/c"))
	require.False(t, msg.TopicExists([]byte("/a/b/#/c")), "Topic should not exist.")
}

func TestSubscribeMessageDecode(t *testing.T) {
	msgBytes := []byte{
		byte(SUBSCRIBE<<4) | 2,
		36,
		0, // packet ID MSB (0)
		7, // packet ID LSB (7)
		0, // topic name MSB (0)
		7, // topic name LSB (7)
		's', 'u', 'r', 'g', 'e', 'm', 'q',
		0, // QoS
		0, // topic name MSB (0)
		8, // topic name LSB (8)
		'/', 'a', '/', 'b', '/', '#', '/', 'c',
		1,  // QoS
		0,  // topic name MSB (0)
		10, // topic name LSB (10)
		'/', 'a', '/', 'b', '/', '#', '/', 'c', 'd', 'd',
		2, // QoS
	}

	msg := NewSubscribeMessage()
	n, err := msg.Decode(msgBytes)

	require.NoError(t, err, "Error decoding message.")
	require.Equal(t, len(msgBytes), n, "Error decoding message.")
	require.Equal(t, SUBSCRIBE, msg.Type(), "Error decoding message.")
	require.Equal(t, 3, len(msg.Topics()), "Error decoding topics.")
	require.True(t, msg.TopicExists([]byte("goflymq")), "Topic 'goflymq' should exist.")
	require.Equal(t, 0, int(msg.TopicQos([]byte("goflymq"))), "Incorrect topic qos.")
	require.True(t, msg.TopicExists([]byte("/a/b/#/c")), "Topic '/a/b/#/c' should exist.")
	require.Equal(t, 1, int(msg.TopicQos([]byte("/a/b/#/c"))), "Incorrect topic qos.")
	require.True(t, msg.TopicExists([]byte("/a/b/#/cdd")), "Topic '/a/b/#/c' should exist.")
	require.Equal(t, 2, int(msg.TopicQos([]byte("/a/b/#/cdd"))), "Incorrect topic qos.")
}

// test empty topic list
func TestSubscribeMessageDecode2(t *testing.T) {
	msgBytes := []byte{
		byte(SUBSCRIBE<<4) | 2,
		2,
		0, // packet ID MSB (0)
		7, // packet ID LSB (7)
	}

	msg := NewSubscribeMessage()
	_, err := msg.Decode(msgBytes)

	require.Error(t, err)
}

func TestSubscribeMessageEncode(t *testing.T) {
	msgBytes := []byte{
		byte(SUBSCRIBE<<4) | 2,
		36,
		0, // packet ID MSB (0)
		7, // packet ID LSB (7)
		0, // topic name MSB (0)
		7, // topic name LSB (7)
		's', 'u', 'r', 'g', 'e', 'm', 'q',
		0, // QoS
		0, // topic name MSB (0)
		8, // topic name LSB (8)
		'/', 'a', '/', 'b', '/', '#', '/', 'c',
		1,  // QoS
		0,  // topic name MSB (0)
		10, // topic name LSB (10)
		'/', 'a', '/', 'b', '/', '#', '/', 'c', 'd', 'd',
		2, // QoS
	}

	msg := NewSubscribeMessage()
	msg.SetPacketId(7)
	msg.AddTopic([]byte("goflymq"), 0)
	msg.AddTopic([]byte("/a/b/#/c"), 1)
	msg.AddTopic([]byte("/a/b/#/cdd"), 2)

	dst := make([]byte, 100)
	n, err := msg.Encode(dst)

	require.NoError(t, err, "Error decoding message.")
	require.Equal(t, len(msgBytes), n, "Error decoding message.")
	require.Equal(t, msgBytes, dst[:n], "Error decoding message.")
}

// test to ensure encoding and decoding are the same
// decode, encode, and decode again
func TestSubscribeDecodeEncodeEquiv(t *testing.T) {
	msgBytes := []byte{
		byte(SUBSCRIBE<<4) | 2,
		36,
		0, // packet ID MSB (0)
		7, // packet ID LSB (7)
		0, // topic name MSB (0)
		7, // topic name LSB (7)
		's', 'u', 'r', 'g', 'e', 'm', 'q',
		0, // QoS
		0, // topic name MSB (0)
		8, // topic name LSB (8)
		'/', 'a', '/', 'b', '/', '#', '/', 'c',
		1,  // QoS
		0,  // topic name MSB (0)
		10, // topic name LSB (10)
		'/', 'a', '/', 'b', '/', '#', '/', 'c', 'd', 'd',
		2, // QoS
	}

	msg := NewSubscribeMessage()
	n, err := msg.Decode(msgBytes)

	require.NoError(t, err, "Error decoding message.")
	require.Equal(t, len(msgBytes), n, "Error decoding message.")

	dst := make([]byte, 100)
	n2, err := msg.Encode(dst)

	require.NoError(t, err, "Error decoding message.")
	require.Equal(t, len(msgBytes), n2, "Error decoding message.")
	require.Equal(t, msgBytes, dst[:n2], "Error decoding message.")

	n3, err := msg.Decode(dst)

	require.NoError(t, err, "Error decoding message.")
	require.Equal(t, len(msgBytes), n3, "Error decoding message.")
}
