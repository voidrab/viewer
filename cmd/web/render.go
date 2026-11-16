//go:build js && wasm

package main

import (
	"context"
	"slices"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/ui/platform/web"
)

// maxOpen is how many documents a render worker keeps open, the most
// recently used; each holds its file in the worker's memory.
const maxOpen = 8

// openDoc is a document a render worker has open, at one version of its file.
type openDoc struct {
	key string
	doc *gopdfrab.Document
}

// serveRenders renders pages for the viewer, which runs in another worker,
// until the page closes. The page sends a job only once the last is answered,
// so renders run one at a time.
func serveRenders() {
	docs := map[string]*openDoc{}
	var recent []string // the paths of docs, the most recently used last
	for job := range web.RenderJobs() {
		var dropped []string
		d := docs[job.Path]
		if d != nil && d.key != job.Key { // the file changed, as converting in place does
			d.doc.Close()
			d = nil
		}
		if d == nil {
			doc, err := gopdfrab.Open(job.Path)
			if err != nil {
				delete(docs, job.Path)
				recent = slices.DeleteFunc(recent, func(p string) bool { return p == job.Path })
				web.Finish(job.ID, nil, err, nil)
				continue
			}
			d = &openDoc{key: job.Key, doc: doc}
			docs[job.Path] = d
		}
		recent = append(slices.DeleteFunc(recent, func(p string) bool { return p == job.Path }), job.Path)
		for len(recent) > maxOpen {
			old := recent[0]
			recent = recent[1:]
			docs[old].doc.Close()
			delete(docs, old)
			web.Forget(old)
			dropped = append(dropped, old)
		}
		p, err := d.doc.Page(job.Page)
		if err != nil {
			web.Finish(job.ID, nil, err, dropped)
			continue
		}
		img, err := p.Render(context.Background(), gopdfrab.RenderOptions{Scale: job.Scale, Region: job.Region})
		web.Finish(job.ID, img, err, dropped)
	}
}
