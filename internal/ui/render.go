package ui

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type Renderer struct {
	schema *jsonschema.Schema
}

func NewRenderer(schemaPath string) (*Renderer, error) {
	schema, err := jsonschema.NewCompiler().Compile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("compile UI schema: %w", err)
	}
	return &Renderer{schema: schema}, nil
}

func (rd *Renderer) Render(r Response) ([]byte, error) {
	out, err := json.Marshal(r)
	if err == nil {
		err = rd.validate(out)
	}
	if err != nil {
		return fallback, fmt.Errorf("response rejected: %w", err)
	}
	return out, nil
}

func (rd *Renderer) validate(doc []byte) error {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(doc))
	if err != nil {
		return err
	}
	return rd.schema.Validate(v)
}

var fallback = func() []byte {
	b, err := json.Marshal(Response{
		Version: "1",
		Blocks:  []Block{Error{Type: "error", Code: "render_failed", Message: "The response could not be displayed. Please try again."}},
		Audit:   Audit{Decision: Refused, Reason: "response failed schema validation"},
	})
	if err != nil {
		panic(err)
	}
	return b
}()
