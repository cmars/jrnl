package main

import (
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/cayleygraph/cayley"
	"github.com/cayleygraph/cayley/graph"
	_ "github.com/cayleygraph/cayley/graph/bolt"
	"github.com/cayleygraph/cayley/graph/iterator"
	"github.com/cayleygraph/cayley/quad"
	"github.com/cayleygraph/cayley/schema"
	"github.com/olebedev/when"
	"github.com/olebedev/when/rules/common"
	"github.com/olebedev/when/rules/en"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
)

func main() {
	store, err := openStore()
	if err != nil {
		log.Fatalln("failed to open store:", err)
	}
	defer store.Close()
	log.Println(store)

	j := NewJournal(store)

	cmdPut := &cobra.Command{
		Use: "put",
		Run: func(cmd *cobra.Command, args []string) {
			buf, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				log.Println("warning:", err)
			}
			if len(buf) == 0 {
				log.Println("warning: empty journal input, nothing to store")
				return
			}
			err = j.AddEntry(string(buf))
			if err != nil {
				log.Fatalln(err)
			}
		},
	}
	var getOptions GetOptions
	cmdGet := &cobra.Command{
		Use: "get",
		Run: func(cmd *cobra.Command, args []string) {
			timeSpec := strings.Join(args, " ")
			if timeSpec != "" {
				w := when.New(nil)
				w.Add(en.All...)
				w.Add(common.All...)
				timeResult, err := w.Parse(timeSpec, time.Now())
				if err != nil || timeResult == nil {
					log.Fatalf("failed to parse time from %q", timeSpec)
				}
				log.Println(timeResult.Time)
				after := timeResult.Time.Truncate(time.Hour * time.Duration(24))
				before := after.Add(time.Hour * time.Duration(24))
				getOptions.After = &after
				getOptions.Before = &before
			}
			results, err := j.Get(&getOptions)
			if err != nil {
				log.Fatalln(err)
			}
			for _, result := range results {
				log.Println(result)
			}
		},
	}
	cmdRoot := &cobra.Command{
		Use: "jrnl",
	}
	cmdRoot.AddCommand(cmdPut)
	cmdRoot.AddCommand(cmdGet)
	err = cmdRoot.Execute()
	if err != nil {
		log.Fatalln(err)
	}
}

type Entry struct {
	ID        quad.IRI  `json:"@id"`
	CreatedAt time.Time `json:"created-at"`
	Contents  string    `json:"contents"`
}

func NewEntry(contents string) *Entry {
	return &Entry{
		ID:        quad.IRI(uuid.New()),
		CreatedAt: time.Now().UTC(),
		Contents:  contents,
	}
}

type Journal struct {
	store *cayley.Handle
}

func NewJournal(store *cayley.Handle) *Journal {
	return &Journal{store: store}
}

func (j *Journal) AddEntry(contents string) error {
	entry := NewEntry(contents)
	writer := graph.NewWriter(j.store.QuadWriter)
	postID, err := schema.WriteAsQuads(writer, entry)
	if err != nil {
		// TODO: save in temp file?
		return errors.Wrap(err, "failed to store entry")
	}
	err = j.store.AddQuad(quad.Make(postID, quad.IRI("is-a"), quad.IRI("journal-entry"), nil))
	if err != nil {
		return errors.Wrap(err, "failed to store entry metadata")
	}
	err = writer.Flush()
	if err != nil {
		return errors.Wrap(err, "failed to write entry")
	}
	return nil
}

type GetOptions struct {
	Before *time.Time
	After  *time.Time
}

func (j *Journal) Get(options *GetOptions) ([]Entry, error) {
	p := cayley.StartPath(j.store.QuadStore).Has(quad.IRI("is-a"), quad.IRI("journal-entry")).Tag("entry")
	if options.Before != nil {
		p = p.Out(quad.IRI("created-at")).Filter(iterator.CompareLT, quad.Time(*options.Before)).Back("entry")
	}
	if options.After != nil {
		p = p.Out(quad.IRI("created-at")).Filter(iterator.CompareGTE, quad.Time(*options.After)).Back("entry")
	}
	var results []Entry
	err := schema.LoadIteratorTo(nil, j.store, reflect.ValueOf(&results), p.BuildIterator())
	if err != nil {
		return nil, errors.Wrap(err, "failed to query")
	}
	return results, nil
}

func openStore() (*cayley.Handle, error) {
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		return nil, errors.New("cannot find HOME directory")
	}
	jrnlPath := filepath.Join(homeDir, ".jrnl.db")

	// Initialize the database
	if _, err := os.Stat(jrnlPath); os.IsNotExist(err) {
		err = graph.InitQuadStore("bolt", jrnlPath, nil)
		if err != nil {
			return nil, errors.Wrap(err, "failed to initialize database")
		}
	} else if err != nil {
		return nil, errors.Wrap(err, "failed to stat database file")
	}

	// Create a brand new graph
	store, err := cayley.NewGraph("bolt", jrnlPath, nil)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create graph")
	}
	return store, nil
}
