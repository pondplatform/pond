package api

import (
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// bindJSON decodes the request body into v, rejecting unknown fields. On
// failure it writes a 400 that says what was wrong and returns false.
func bindJSON(c *gin.Context, v any) bool {
	if err := decodeJSON(c.Request.Body, v); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func decodeJSON(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil {
		return nil
	}

	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.EOF):
		return errors.New("request body is empty; expected a JSON object")
	case errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("request body is not valid JSON: unexpected end of input")
	case errors.As(err, &syntaxErr):
		return fmt.Errorf("request body is not valid JSON: %v (at byte %d)", syntaxErr, syntaxErr.Offset)
	case errors.As(err, &typeErr):
		if typeErr.Field == "" {
			return fmt.Errorf("request body must be a JSON object, got %s", typeErr.Value)
		}
		return fmt.Errorf("field %q must be %s, got %s", typeErr.Field, describeJSONType(typeErr.Type), typeErr.Value)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return fmt.Errorf("unknown field %s in request body; check the spelling, or whether the client and server versions differ", field)
	default:
		return fmt.Errorf("invalid request body: %v", err)
	}
}

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

func describeJSONType(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeFor[uuid.UUID]() {
		return "a UUID string"
	}
	if reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return "a string"
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "an integer"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	default:
		return "an object"
	}
}

// parseUUIDParam reads a UUID path parameter. On failure it writes a 400
// naming the parameter and the bad value, and returns false.
func parseUUIDParam(c *gin.Context, param, label string) (uuid.UUID, bool) {
	raw := c.Param(param)
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(c, http.StatusBadRequest, fmt.Sprintf("invalid %s %q: expected a UUID", label, raw))
		return uuid.Nil, false
	}
	return id, true
}
