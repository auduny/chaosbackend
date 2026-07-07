package chaosbackend

import (
	"html/template"
	"net/http"
)

// Server serves the chaosbackend HTTP endpoints.
type Server struct {
	tmpl *template.Template
}

// New creates a Server from cfg, parsing the configured (or embedded
// default) template once up front.
func New(cfg Config) (*Server, error) {
	var (
		tmpl *template.Template
		err  error
	)
	if cfg.TemplateFile != "" {
		tmpl, err = template.ParseFiles(cfg.TemplateFile)
	} else {
		tmpl, err = template.New("default").Parse(defaultTemplateHTML)
	}
	if err != nil {
		return nil, err
	}
	return &Server{tmpl: tmpl}, nil
}

// Mux returns a *http.ServeMux with the standard chaosbackend routes registered.
func (s *Server) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", SlowHandler)
	mux.HandleFunc("/error", ErrorHandler)
	mux.HandleFunc("/reset", ResetHandler)
	mux.HandleFunc("/new", FaultHandler)
	mux.Handle("/", AddHeaders(http.HandlerFunc(s.DefaultHandler)))
	return mux
}

// DefaultHandler serves "/" using the Server's configured template.
func (s *Server) DefaultHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Backend", "default")
	s.tmpl.Execute(w, "This is the default page.")
}
