// Package apispec checks outgoing HTTP requests against a provider's
// official OpenAPI document. It is imported only by tests, so it never
// links into the imgen binary.
package apispec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// Spec is a loaded, validated OpenAPI document plus a router.
type Spec struct {
	Doc    *openapi3.T
	router routers.Router
}

// Load reads and validates an OpenAPI 3.x document (JSON or YAML).
func Load(path string) (*Spec, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	if err := doc.Validate(context.Background(), openapi3.DisableExamplesValidation()); err != nil {
		return nil, fmt.Errorf("validate %s: %w", path, err)
	}
	// Route on path only: tests send to httptest servers, not the real host.
	doc.Servers = openapi3.Servers{{URL: "/"}}
	r, err := gorillamux.NewRouter(doc)
	if err != nil {
		return nil, err
	}
	return &Spec{Doc: doc, router: r}, nil
}

// Check validates method, path, parameters, headers and body of req against
// the spec. The request body is restored, so req can still be sent.
// Security requirements are not enforced here; tests assert auth headers
// themselves.
func (s *Spec) Check(req *http.Request) error {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return err
		}
		body = b
		req.Body = io.NopCloser(bytes.NewReader(b))
	}
	probe := req.Clone(context.Background())
	probe.URL = &url.URL{Path: req.URL.Path, RawQuery: req.URL.RawQuery}
	probe.Host = ""
	probe.Body = io.NopCloser(bytes.NewReader(body))
	route, params, err := s.router.FindRoute(probe)
	if err != nil {
		return fmt.Errorf("%s %s: no matching operation in spec: %w", req.Method, req.URL.Path, err)
	}
	in := &openapi3filter.RequestValidationInput{
		Request: probe, PathParams: params, Route: route,
		Options: &openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			MultiError:         true,
		},
	}
	if err := openapi3filter.ValidateRequest(context.Background(), in); err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	return nil
}

// Operations lists "METHOD /path" for every operation, for coverage notes.
func (s *Spec) Operations() []string {
	var out []string
	for path, item := range s.Doc.Paths.Map() {
		for method := range item.Operations() {
			out = append(out, strings.ToUpper(method)+" "+path)
		}
	}
	return out
}
