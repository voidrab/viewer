//go:build js && wasm

package main

import (
	"context"
	"os"
	"slices"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/ui/platform/web"
)

// maxOpen is the number of documents a render worker keeps open. When more
// are open, the least recently used is closed. Each holds its file in memory.
const maxOpen = 8

// openDoc is a document a render worker has open, at one version of its file.
type openDoc struct {
	path, key string
	doc       *gopdfrab.Document
}

// serveRenders renders pages for the viewer, which runs in another worker. It
// does not return. The page sends this worker one job at a time, the next
// after the last is answered.
func serveRenders() {
	var docs []openDoc // the most recently used last
	for job := range web.RenderJobs() {
		var dropped []string
		i := slices.IndexFunc(docs, func(d openDoc) bool { return d.path == job.Path })
		var d openDoc
		if i >= 0 {
			d = docs[i]
			docs = slices.Delete(docs, i, i+1)
			if d.key != job.Key { // the file was changed since it was opened
				d.doc.Close()
				d.doc = nil
			}
		}
		if d.doc == nil {
			doc, err := open(job.Path)
			if err != nil {
				web.Finish(job.ID, nil, err, nil)
				continue
			}
			d = openDoc{path: job.Path, key: job.Key, doc: doc}
		}
		docs = append(docs, d)
		for len(docs) > maxOpen {
			docs[0].doc.Close()
			dropped = append(dropped, docs[0].path)
			docs = docs[1:]
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

// open parses the document at path from its bytes in memory. On the web this
// is faster than reading through the in-memory file system. It then removes
// the file, so that the document's bytes are held once.
func open(path string) (*gopdfrab.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := gopdfrab.OpenBytes(data)
	if err != nil {
		return nil, err
	}
	web.Forget(path)
	return doc, nil
}
