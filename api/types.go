package api

import (
	"bytes"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/infrahq/infra/internal/validate"
	"github.com/infrahq/infra/uid"
)

type Query map[string][]string

type Resource struct {
	ID uid.ID `uri:"id"`
}

func (r Resource) ValidationRules() []validate.ValidationRule {
	return []validate.ValidationRule{
		validate.Required("id", r.ID),
	}
}

// IDOrSelf is a union type that may represent either a uid.ID or the literal
// string "self".
type IDOrSelf struct {
	ID     uid.ID
	IsSelf bool
}

func (i *IDOrSelf) UnmarshalText(b []byte) error {
	if bytes.Equal(b, []byte("self")) {
		i.IsSelf = true
		return nil
	}
	var err error
	i.ID, err = uid.Parse(b)
	return err
}

func (i IDOrSelf) DescribeSchema(schema *openapi3.Schema) {
	schema.Type = "string"
	schema.Format = "uid|self"
	schema.Pattern = `[\da-zA-HJ-NP-Z]{1,11}|self`
	schema.Example = "4yJ3n3D8E2"
	schema.Description = "a uid or the literal self"
}

type Time time.Time

func (t Time) MarshalJSON() ([]byte, error) {
	if time.Time(t).IsZero() {
		return []byte("null"), nil
	}
	s := time.Time(t).UTC().Format(time.RFC3339)
	return []byte(`"` + s + `"`), nil
}

func (t *Time) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	if string(data) == `""` {
		return nil
	}
	s := strings.Trim(string(data), `"`)
	tmp, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return err
	}
	*t = Time(tmp.UTC())
	return nil
}

func (t Time) String() string {
	return time.Time(t).Format(time.RFC3339)
}

func (t Time) Format(layout string) string {
	return time.Time(t).Format(layout)
}

func (t Time) Equal(other Time) bool {
	return time.Time(t).Equal(time.Time(other))
}

func (t Time) Time() time.Time {
	return time.Time(t)
}

type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Duration(d).String() + `"`), nil
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(dur)
	return nil
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

func (t Time) DescribeSchema(schema *openapi3.Schema) {
	schema.Type = "string"
	schema.Format = "date-time" // date-time is rfc3339
	schema.Example = time.Date(2022, 3, 14, 9, 48, 0, 0, time.UTC).Format(time.RFC3339)
	if len(schema.Description) == 0 {
		schema.Description = "formatted as an RFC3339 date-time"
	}
}

func (d Duration) DescribeSchema(schema *openapi3.Schema) {
	schema.Type = "string"
	schema.Format = "duration"
	schema.Example = "72h3m6.5s"
	if len(schema.Description) == 0 {
		schema.Description = "a duration of time supporting (h)ours, (m)inutes, and (s)econds"
	}
}

type ListResponse[T any] struct {
	PaginationResponse `json:",inline"`
	Count              int `json:"count"`
	Items              []T `json:"items"`
}

func NewListResponse[T, M any](items []M, pr PaginationResponse, fn func(item M) T) *ListResponse[T] {
	result := &ListResponse[T]{
		Items:              make([]T, 0, len(items)),
		Count:              len(items),
		PaginationResponse: pr,
	}

	for _, item := range items {
		result.Items = append(result.Items, fn(item))
	}

	return result
}

// PEM is a base64 encoded string, commonly used to store certificates and
// private keys. PEM values will be normalized to remove any leading whitespace
// and all but a single trailing newline.
type PEM string
