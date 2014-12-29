package ldclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"sync"
	"time"
)

type eventProcessor struct {
	queue  []Event
	apiKey string
	config Config
	mu     *sync.Mutex
	client *http.Client
	closer chan struct{}
}

type Event interface {
	GetBase() BaseEvent
	GetKind() string
}

type BaseEvent struct {
	CreationDate uint64 `json:"creationDate"`
	Key          string `json:"key"`
	Kind         string `json:"kind"`
	User         User   `json:"user"`
}

type FeatureRequestEvent struct {
	BaseEvent
	Value interface{} `json:"value"`
}

const (
	FEATURE_REQUEST_EVENT = "feature"
	CUSTOM_EVENT          = "custom"
)

func newEventProcessor(apiKey string, config Config) *eventProcessor {
	res := &eventProcessor{
		queue:  make([]Event, 0),
		apiKey: apiKey,
		config: config,
		client: &http.Client{},
		closer: make(chan struct{}),
		mu:     &sync.Mutex{},
	}

	go func() {
		if err := recover(); err != nil {
			res.config.Logger.Printf("Unexpected panic in event processing thread: %+v", err)
		}

		ticker := time.NewTicker(config.FlushInterval)
		for {
			select {
			case <-ticker.C:
				res.flush()
			case <-res.closer:
				ticker.Stop()
				return
			}
		}
	}()

	return res
}

func (ep *eventProcessor) close() {
	close(ep.closer)
	ep.flush()
}

func (ep *eventProcessor) flush() {
	ep.mu.Lock()

	if len(ep.queue) == 0 {
		ep.mu.Unlock()
		return
	}

	events := ep.queue
	ep.mu.Unlock()

	ep.queue = make([]Event, 0)

	payload, marshalErr := json.Marshal(events)

	if marshalErr != nil {
		ep.config.Logger.Printf("Unexpected error marshalling event json: %+v", marshalErr)
	}

	req, reqErr := http.NewRequest("POST", ep.config.BaseUri+"/api/events/bulk", bytes.NewReader(payload))

	if reqErr != nil {
		ep.config.Logger.Printf("Unexpected error while creating event request: %+v", reqErr)
	}

	req.Header.Add("Authorization", "api_key "+ep.apiKey)
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", "GoClient/"+Version)

	resp, respErr := ep.client.Do(req)

	if respErr != nil {
		ep.config.Logger.Printf("Unexpected error while sending events: %+v", respErr)
	}

	if resp.Body != nil {
		ioutil.ReadAll(resp.Body)
		resp.Body.Close()
	}

}

func (ep *eventProcessor) sendEvent(evt Event) error {
	ep.mu.Lock()
	defer ep.mu.Unlock()

	if len(ep.queue) >= ep.config.Capacity {
		return errors.New("Exceeded event queue capacity. Increase capacity to avoid dropping events.")
	}
	ep.queue = append(ep.queue, evt)
	return nil
}

func newFeatureRequestEvent(key string, user User, value interface{}) FeatureRequestEvent {
	return FeatureRequestEvent{
		BaseEvent: BaseEvent{
			CreationDate: now(),
			Key:          key,
			User:         user,
			Kind:         FEATURE_REQUEST_EVENT,
		},
		Value: value,
	}
}

func (evt FeatureRequestEvent) GetBase() BaseEvent {
	return evt.BaseEvent
}

func (evt FeatureRequestEvent) GetKind() string {
	return evt.Kind
}

type CustomEvent struct {
	BaseEvent
	Data interface{} `json:"data"`
}

func newCustomEvent(key string, user User, data interface{}) CustomEvent {
	return CustomEvent{
		BaseEvent: BaseEvent{
			CreationDate: now(),
			Key:          key,
			User:         user,
			Kind:         CUSTOM_EVENT,
		},
		Data: data,
	}
}

func (evt CustomEvent) GetBase() BaseEvent {
	return evt.BaseEvent
}

func (evt CustomEvent) GetKind() string {
	return evt.Kind
}

func now() uint64 {
	return toUnixMillis(time.Now())
}

func toUnixMillis(t time.Time) uint64 {
	ms := time.Duration(t.UnixNano()) / time.Millisecond

	return uint64(ms)
}