package sessionsv5

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/huanglishi/gofly-mqttv5/logger"
)

var (
	ErrSessionsProviderNotFound = errors.New("Session: Session provider not found")
	ErrKeyNotAvailable          = errors.New("Session: not item found for key.")

	providers = make(map[string]SessionsProvider)
)

// Register makes a session provider available by the provided name.
// If a Register is called twice with the same name or if the driver is nil,
// it panics.
var Default = "default"

func Register(name string, provider SessionsProvider) {
	if provider == nil {
		panic("session: Register provide is nil")
	}
	if name == "" {
		name = Default
	}
	if _, dup := providers[name]; dup {
		panic("session: Register called twice for provider " + name)
	}
	logger.Logger.Infof("Register SessionProvide：'%s' success，%T", name, provider)
	providers[name] = provider
}

func Unregister(name string) {
	if name == "" {
		name = Default
	}
	delete(providers, name)
}

type Manager struct {
	p SessionsProvider
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

func (this *Manager) New(id string, cleanStart bool, expiryTime uint32) (Session, error) {
	if id == "" {
		id = this.sessionId()
	}
	return this.p.New(id, cleanStart, expiryTime)
}

func (this *Manager) Get(id string, cleanStart bool, expiryTime uint32) (Session, error) {
	return this.p.Get(id, cleanStart, expiryTime)
}

func (this *Manager) Del(id string) {
	this.p.Del(id)
}

func (this *Manager) Save(id string) error {
	return this.p.Save(id)
}

func (this *Manager) Count() int {
	return this.p.Count()
}

func (this *Manager) Close() error {
	return this.p.Close()
}

func (manager *Manager) sessionId() string {
	b := make([]byte, 15)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(b)
}
