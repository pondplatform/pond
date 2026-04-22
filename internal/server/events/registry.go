package events

import (
	"encoding/json"
	"fmt"
	"reflect"
)

var typeRegistry = map[string]reflect.Type{}

func init() {
	for _, v := range []any{
		CommandQueued{},
		CommandResult{},
		CommandLog{},
		CommandDispatch{},
		AgentReady{},
		CommandStarted{},
		AgentDisconnected{},
		UserInputRequired{},
		UserInputProvided{},
	} {
		t := reflect.TypeOf(v)
		typeRegistry[t.Name()] = t
	}
}

type envelope struct {
	TypeName string          `json:"t"`
	Data     json.RawMessage `json:"d"`
}

func marshalEvent(v any) ([]byte, error) {
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{TypeName: t.Name(), Data: data})
}

func unmarshalEvent(b []byte) (any, error) {
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	rt, ok := typeRegistry[env.TypeName]
	if !ok {
		return nil, fmt.Errorf("unknown event type %q", env.TypeName)
	}
	ptr := reflect.New(rt)
	if err := json.Unmarshal(env.Data, ptr.Interface()); err != nil {
		return nil, err
	}
	return ptr.Elem().Interface(), nil
}
