package main

import (
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"web-server/src/client"
	"web-server/src/tracer"
)

type TemplateHandler struct {
	once     sync.Once
	template *template.Template
	filename string
}

func (t *TemplateHandler) ServeHTTP(writer http.ResponseWriter, req *http.Request) {
	t.once.Do(func() {
		templatePath := filepath.Join("src", "templates", t.filename)
		t.template = template.Must(template.ParseFiles(templatePath))
	})

	data := struct {
		Host string
	}{
		Host: req.Host,
	}

	t.template.Execute(writer, data)
}

func main() {
	var addr = flag.String("addr", ":8080", "The addr of the app")
	flag.Parse()

	r := client.NewRoom()
	r.Tracer = tracer.New(os.Stdout)

	http.Handle("/", &TemplateHandler{filename: "index.html"})
	http.Handle("/room", r)

	go r.Run()

	fmt.Printf("Сервер запущен по порту: %s\n", *addr)
	if err := http.ListenAndServe(*addr, nil); err != nil {
		fmt.Fprintf(os.Stderr, "ListenAndServe: %v\n", err)
	}
}
