package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-playground/form"
)

func (s *Server) writeJSON(w http.ResponseWriter, status int, data any, headers http.Header) error {
	js, err := json.Marshal(data)
	if err != nil {
		return err
	}

	js = append(js, '\n')

	for k, values := range headers {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if _, err := w.Write(js); err != nil {
		return err
	}

	return nil
}

func (s *Server) readJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	maxBytes := int64(1_048_576) // 1MB
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err != nil {
		var syntaxError *json.SyntaxError
		var unmarshalTypeError *json.UnmarshalTypeError
		var invalidUnmarshalError *json.InvalidUnmarshalError
		var maxBytesError *http.MaxBytesError

		switch {
		case errors.As(err, &syntaxError):
			return &MalformedRequest{Msg: fmt.Sprintf("body contains badly-formed JSON (at character %d)", syntaxError.Offset)}

		case errors.Is(err, io.ErrUnexpectedEOF):
			return &MalformedRequest{Msg: "body contains badly-formed JSON"}

		case errors.As(err, &unmarshalTypeError):
			if unmarshalTypeError.Field != "" {
				return &MalformedRequest{Msg: fmt.Sprintf("body contains incorrect JSON type for field %q", unmarshalTypeError.Field)}
			}
			return &MalformedRequest{Msg: fmt.Sprintf("body contains incorrect JSON type (at character %d)", unmarshalTypeError.Offset)}

		case errors.Is(err, io.EOF):
			return &MalformedRequest{Msg: "body must not be empty"}

		// encoding/json has no typed error for unknown fields, so match the message prefix.
		case strings.HasPrefix(err.Error(), "json: unknown field "):
			fieldName := strings.TrimPrefix(err.Error(), "json: unknown field ")
			return &MalformedRequest{Msg: fmt.Sprintf("body contains unknown key %s", fieldName)}

		case errors.As(err, &maxBytesError):
			return &MalformedRequest{Msg: fmt.Sprintf("body must not be larger than %d bytes", maxBytesError.Limit)}

		case errors.As(err, &invalidUnmarshalError):
			panic(err)

		default:
			return err
		}
	}

	err = dec.Decode(&struct{}{})
	if !errors.Is(err, io.EOF) {
		return &MalformedRequest{Msg: "body must only contain a single JSON value"}
	}

	return nil
}

func (s *Server) logError(r *http.Request, err error) {
	var (
		method = r.Method
		uri    = r.URL.RequestURI()
	)

	s.logger.Error(err.Error(), "method", method, "uri", uri)
}

func (s *Server) decodePostForm(r *http.Request, dst any) error {
	err := r.ParseForm()
	if err != nil {
		return err
	}

	err = s.formDecoder.Decode(dst, r.PostForm)
	if err != nil {
		if _, ok := errors.AsType[*form.InvalidDecoderError](err); ok {
			panic(err)
		}

		return err
	}

	return nil
}
