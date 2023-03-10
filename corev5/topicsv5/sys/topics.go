// 共享订阅
package sys

import (
	"errors"
	"fmt"

	"github.com/huanglishi/gofly-mqttv5/corev5/messagev5"
	"github.com/huanglishi/gofly-mqttv5/corev5/topicsv5"
	"github.com/huanglishi/gofly-mqttv5/logger"
)

const (
	// MWC is the multi-level wildcard
	MWC = "#"

	// SWC is the single level wildcard
	SWC = "+"

	// SEP is the topic level separator
	SEP = "/"

	// SYS is the starting character of the system level topics
	//SYS是系统级主题的起始字符
	SYS = "$"

	// Both wildcards
	_WC = "#+"
)

var (
	// ErrAuthFailure is returned when the user/pass supplied are invalid
	ErrAuthFailure = errors.New("auth: Authentication failure")

	// ErrAuthProviderNotFound is returned when the requested provider does not exist.
	// It probably hasn't been registered yet.
	ErrAuthProviderNotFound = errors.New("auth: Authentication provider not found")

	providers = make(map[string]SysTopicsProvider)
)

// TopicsProvider
type SysTopicsProvider interface {
	Subscribe(subs topicsv5.Sub, subscriber interface{}) (byte, error)
	Unsubscribe(topic []byte, subscriber interface{}) error
	Subscribers(topic []byte, qos byte, subs *[]interface{}, qoss *[]topicsv5.Sub) error
	Retain(msg *messagev5.PublishMessage) error
	Retained(topic []byte, msgs *[]*messagev5.PublishMessage) error
	Close() error
}

var Default = "default"

func Register(name string, provider SysTopicsProvider) {
	if provider == nil {
		panic("sys topics: Register provide is nil")
	}
	if name == "" {
		name = Default
	}
	if _, dup := providers[name]; dup {
		panic("sys topics: Register called twice for provider " + name)
	}

	providers[name] = provider
	logger.Logger.Infof("Register Sys TopicsProvider：'%s' success，%T", name, provider)
}

func Unregister(name string) {
	if name == "" {
		name = Default
	}
	delete(providers, name)
}

type Manager struct {
	p SysTopicsProvider
}

func NewManager(providerName string) (*Manager, error) {
	if providerName == "" {
		providerName = Default
	}
	p, ok := providers[providerName]
	if !ok {
		return nil, fmt.Errorf("session: unknown provider %q", providerName)
	}

	return &Manager{p: p}, nil
}

func (this *Manager) Subscribe(subs topicsv5.Sub, subscriber interface{}) (byte, error) {
	return this.p.Subscribe(subs, subscriber)
}

func (this *Manager) Unsubscribe(topic []byte, subscriber interface{}) error {
	return this.p.Unsubscribe(topic, subscriber)
}

func (this *Manager) Subscribers(topic []byte, qos byte, subs *[]interface{}, qoss *[]topicsv5.Sub) error {
	return this.p.Subscribers(topic, qos, subs, qoss)
}

func (this *Manager) Retain(msg *messagev5.PublishMessage) error {
	return this.p.Retain(msg)
}

func (this *Manager) Retained(topic []byte, msgs *[]*messagev5.PublishMessage) error {
	return this.p.Retained(topic, msgs)
}

func (this *Manager) Close() error {
	return this.p.Close()
}
