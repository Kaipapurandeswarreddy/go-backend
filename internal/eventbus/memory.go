package eventbus

import (
	"encoding/json"
	"sync"

	"ambigo-backend/interfaces"
	"ambigo-backend/internal/logger"
	"ambigo-backend/internal/metrics"
)

type InMemoryBus struct {
	mu          sync.RWMutex
	subscribers map[string][]chan []byte
}

func NewInMemoryBus() *InMemoryBus {
	return &InMemoryBus{
		subscribers: make(map[string][]chan []byte),
	}
}

func (b *InMemoryBus) Publish(channel string, payload []byte) error {
	b.mu.RLock()
	channels := b.subscribers[channel]
	b.mu.RUnlock()

	payloadCopy := make([]byte, len(payload))
	copy(payloadCopy, payload)

	for _, ch := range channels {
		select {
		case ch <- payloadCopy:
		default:
			metrics.EventBusMessagesDropped.WithLabelValues(channel).Inc()
			logger.Log.Warn().Str("channel", channel).Msg("Dropping message: subscriber too slow")
		}
	}
	return nil
}

// PublishEvent marshals a struct and publishes it on the given channel.
func (b *InMemoryBus) PublishEvent(channel string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// Inject the channel name into the payload so subscribers (e.g. AuditLogger)
	// can record which channel an event came from.
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err == nil && obj != nil {
		obj["_channel"] = channel
		if data, err = json.Marshal(obj); err != nil {
			return err
		}
	}
	return b.Publish(channel, data)
}

const defaultWorkerPoolSize = 10

func (b *InMemoryBus) Subscribe(channel string, handler func(payload []byte)) error {
	ch := make(chan []byte, 512)

	b.mu.Lock()
	b.subscribers[channel] = append(b.subscribers[channel], ch)
	b.mu.Unlock()

	// 10-worker pool draining the same channel: at 200 rps a single sequential
	// handler (FCM 100ms each) needed 50s for 500 offers; 10 workers brings it
	// to ~5s. Each worker competes for messages — each message processed exactly once.
	for i := 0; i < defaultWorkerPoolSize; i++ {
		go func(workerID int) {
			defer func() {
				if r := recover(); r != nil {
					logger.Log.Error().Interface("panic", r).Str("channel", channel).Int("worker", workerID).Msg("Panic in subscriber")
				}
			}()
			for msg := range ch {
				func() {
					defer func() {
						if r := recover(); r != nil {
							logger.Log.Error().Interface("panic", r).Str("channel", channel).Int("worker", workerID).Msg("Panic in subscriber handler")
						}
					}()
					handler(msg)
				}()
			}
		}(i)
	}
	return nil
}

func (b *InMemoryBus) Unsubscribe(channel string) error {
	b.mu.Lock()
	delete(b.subscribers, channel)
	b.mu.Unlock()
	return nil
}

func (b *InMemoryBus) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, channels := range b.subscribers {
		for _, ch := range channels {
			close(ch)
		}
	}
	b.subscribers = make(map[string][]chan []byte)
	return nil
}

func (b *InMemoryBus) SubscribeWithChan(channel string) chan []byte {
	ch := make(chan []byte, 512)
	b.mu.Lock()
	b.subscribers[channel] = append(b.subscribers[channel], ch)
	b.mu.Unlock()
	return ch
}

var _ interfaces.EventBus = (*InMemoryBus)(nil)
