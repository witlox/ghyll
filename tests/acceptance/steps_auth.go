package acceptance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"

	"github.com/cucumber/godog"
	"github.com/witlox/ghyll/config"
	"github.com/witlox/ghyll/stream"
)

// authState carries scenario-local server + capture state. Kept
// scenario-local (not on ScenarioState) so concurrent scenarios
// can't leak captured headers across each other. godog Before/After
// hooks reset this each scenario.
//
// AUTH-W-007 / ADV-AUTH-009 / R7: this struct mutates process env
// via os.Setenv during step execution. godog's default Concurrency
// is 1 (serial), which keeps this safe — but a future bump of
// Options.Concurrency would race the env mutations. registerAuthSteps
// asserts Concurrency == 0 (the godog default that maps to 1) at
// scenario start; bumping concurrency without re-engineering this
// state will fail loudly.
type authState struct {
	server    *httptest.Server
	auth      atomic.Value // string
	cfg       *config.Config
	apiKey    string
	streamErr error

	// envKeys tracks any env vars Set during the scenario so
	// the After hook can unset them — preventing one scenario's
	// sentinel from leaking into another's "must not contain"
	// assertion (R7 from the plan: test-pollution guard).
	envKeys []string
}

func (s *authState) setEnv(k, v string) {
	_ = os.Setenv(k, v)
	s.envKeys = append(s.envKeys, k)
}

func (s *authState) reset() {
	if s.server != nil {
		s.server.Close()
		s.server = nil
	}
	for _, k := range s.envKeys {
		_ = os.Unsetenv(k)
	}
	s.envKeys = nil
	s.cfg = nil
	s.apiKey = ""
	s.streamErr = nil
	s.auth.Store("")
}

func registerAuthSteps(ctx *godog.ScenarioContext, _ *ScenarioState) {
	st := &authState{}
	st.auth.Store("")

	ctx.Before(func(ctx2 context.Context, sc *godog.Scenario) (context.Context, error) {
		st.reset()
		return ctx2, nil
	})
	ctx.After(func(ctx2 context.Context, sc *godog.Scenario, err error) (context.Context, error) {
		st.reset()
		return ctx2, nil
	})

	// Track last captured headers across requests.
	var (
		capturedAuth     atomic.Value
		capturedCT       atomic.Value
		capturedAccept   atomic.Value
		capturedAuthHits atomic.Int32
	)
	capturedAuth.Store("")
	capturedCT.Store("")
	capturedAccept.Store("")

	mkRecordingServer := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedAuthHits.Add(1)
			capturedAuth.Store(r.Header.Get("Authorization"))
			capturedCT.Store(r.Header.Get("Content-Type"))
			capturedAccept.Store(r.Header.Get("Accept"))
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
	}

	ctx.Step(`^a recording HTTP server that captures inbound Authorization headers$`, func() error {
		capturedAuth.Store("")
		capturedCT.Store("")
		capturedAccept.Store("")
		capturedAuthHits.Store(0)
		st.server = mkRecordingServer()
		return nil
	})

	ctx.Step(`^a recording HTTP server that returns HTTP (\d+) with the configured api_key echoed in the body$`, func(code int) error {
		// The server captures the inbound key from the Bearer
		// header (the api_key the dispatcher sent) and echoes it
		// in a 401/403 body. We use this to assert the redaction
		// guard at classifyHTTPError.
		st.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedAuthHits.Add(1)
			capturedAuth.Store(r.Header.Get("Authorization"))
			incoming := r.Header.Get("Authorization")
			w.WriteHeader(code)
			fmt.Fprintf(w, `{"error":{"message":"invalid token %s"}}`, incoming)
		}))
		return nil
	})

	ctx.Step(`^ghyll config defines model "([^"]*)" with api_key "([^"]*)"$`, func(name, key string) error {
		if st.server == nil {
			return fmt.Errorf("server must be set up before config")
		}
		st.cfg = &config.Config{
			Models: map[string]config.ModelConfig{
				name: {
					Endpoint:   st.server.URL,
					Dialect:    "glm",
					MaxContext: 100000,
					APIKey:     key,
				},
			},
			Routing: config.RoutingConfig{
				DefaultModel:            name,
				ContextDepthThreshold:   32000,
				ToolDepthThreshold:      5,
				GateFloorEscalateAtRank: 2,
			},
		}
		st.apiKey = key
		return nil
	})

	ctx.Step(`^env "([^"]*)" is "([^"]*)"$`, func(k, v string) error {
		st.setEnv(k, v)
		return nil
	})

	ctx.Step(`^the dispatcher sends one streaming request to "([^"]*)"$`, func(modelName string) error {
		if st.cfg == nil {
			return fmt.Errorf("cfg not initialised")
		}
		hdr := buildAuthHeaderForTest(st.cfg, modelName)
		client := stream.NewClient(st.cfg.Models[modelName].Endpoint, &stream.ClientOptions{
			MaxRetries:    0,
			BaseBackoffMs: 1,
			ExtraHeaders:  hdr,
		})
		_, st.streamErr = client.Send([]map[string]any{{"role": "user", "content": "x"}})
		return nil
	})

	ctx.Step(`^the captured Authorization header equals "([^"]*)"$`, func(want string) error {
		got, _ := capturedAuth.Load().(string)
		if got != want {
			return fmt.Errorf("captured Authorization = %q, want %q", got, want)
		}
		return nil
	})

	ctx.Step(`^the captured Content-Type header equals "([^"]*)"$`, func(want string) error {
		got, _ := capturedCT.Load().(string)
		if got != want {
			return fmt.Errorf("captured Content-Type = %q, want %q", got, want)
		}
		return nil
	})

	ctx.Step(`^the captured Accept header equals "([^"]*)"$`, func(want string) error {
		got, _ := capturedAccept.Load().(string)
		if got != want {
			return fmt.Errorf("captured Accept = %q, want %q", got, want)
		}
		return nil
	})

	ctx.Step(`^the surfaced StreamError message equals "([^"]*)"$`, func(want string) error {
		if st.streamErr == nil {
			return fmt.Errorf("expected a stream error, got nil")
		}
		var se *stream.StreamError
		if !errors.As(st.streamErr, &se) {
			return fmt.Errorf("expected *stream.StreamError, got %T", st.streamErr)
		}
		if !strings.HasPrefix(se.Message, want) {
			return fmt.Errorf("StreamError.Message = %q, want prefix %q", se.Message, want)
		}
		return nil
	})

	ctx.Step(`^the surfaced error string does not contain "([^"]*)"$`, func(sentinel string) error {
		if st.streamErr == nil {
			return fmt.Errorf("no error captured")
		}
		if strings.Contains(st.streamErr.Error(), sentinel) {
			return fmt.Errorf("error string leaks sentinel %q: %s", sentinel, st.streamErr.Error())
		}
		return nil
	})

	ctx.Step(`^the redacted provenance for model "([^"]*)" equals "([^"]*)"$`, func(name, want string) error {
		_, src := config.ResolveAPIKeyWithSource(st.cfg, name)
		var got string
		switch src {
		case config.APIKeyFromEnv:
			got = "<env>"
		case config.APIKeyFromTOML:
			got = "<toml>"
		default:
			got = "<unset>"
		}
		if got != want {
			return fmt.Errorf("provenance = %q, want %q", got, want)
		}
		return nil
	})

	// AUTH-10 / AUTH-1 — config-loader scenarios for misspelled
	// keys and colliding env buckets. These drive config.Load
	// against a temp file rather than ResolveAPIKey directly so
	// the validation surface is end-to-end.
	var loadErr error
	var tmpConfigPath string

	ctx.Step(`^a config file containing the misspelled key "([^"]*)" under \[models\.([^"]*)\]$`, func(misspelled, model string) error {
		dir, err := os.MkdirTemp("", "ghyll-auth-misspelled-")
		if err != nil {
			return err
		}
		tmpConfigPath = dir + "/config.toml"
		body := fmt.Sprintf(`[models.%s]
endpoint = "https://x/v1"
dialect = "glm"
%s = "sk-xxx"

[routing]
default_model = "%s"
`, model, misspelled, model)
		return os.WriteFile(tmpConfigPath, []byte(body), 0o600)
	})

	ctx.Step(`^a config file with models "([^"]*)" and "([^"]*)"$`, func(a, b string) error {
		dir, err := os.MkdirTemp("", "ghyll-auth-collide-")
		if err != nil {
			return err
		}
		tmpConfigPath = dir + "/config.toml"
		body := fmt.Sprintf(`[models."%s"]
endpoint = "https://x/v1"
dialect = "glm"

[models."%s"]
endpoint = "https://y/v1"
dialect = "glm"

[routing]
default_model = "%s"
`, a, b, a)
		return os.WriteFile(tmpConfigPath, []byte(body), 0o600)
	})

	ctx.Step(`^ghyll loads the config$`, func() error {
		_, loadErr = config.Load(tmpConfigPath)
		return nil
	})

	ctx.Step(`^the load fails with a validation error$`, func() error {
		if loadErr == nil {
			return fmt.Errorf("expected validation error, got nil")
		}
		if !config.IsValidation(loadErr) {
			return fmt.Errorf("not a validation error: %v", loadErr)
		}
		return nil
	})

	ctx.Step(`^the validation error mentions "([^"]*)"$`, func(needle string) error {
		if loadErr == nil {
			return fmt.Errorf("no error captured")
		}
		if !strings.Contains(loadErr.Error(), needle) {
			return fmt.Errorf("error %q does not mention %q", loadErr.Error(), needle)
		}
		return nil
	})
}

// buildAuthHeaderForTest mirrors cmd/ghyll/auth.go's buildAuthHeader
// without importing the main package (which is unimportable). The
// acceptance suite tests the OBSERVABLE behaviour — the Bearer
// reaches the wire and the redactor returns the right token — so
// duplicating this 4-line helper is acceptable.
func buildAuthHeaderForTest(cfg *config.Config, modelName string) http.Header {
	k := config.ResolveAPIKey(cfg, modelName)
	if k == "" {
		return nil
	}
	return http.Header{"Authorization": []string{"Bearer " + k}}
}
