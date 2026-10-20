package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func Decode(r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("request must contain exactly one JSON value")
	}
	return nil
}
