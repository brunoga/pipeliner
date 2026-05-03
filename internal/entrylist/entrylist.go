// Package entrylist provides a named persistent list of entries stored via the
// store.Store interface. It is used by the list_add and list_match plugins for
// cross-task coordination.
package entrylist

import "github.com/brunoga/pipeliner/internal/store"

// List wraps a store.Bucket as a named set of entry titles → URLs.
type List struct {
	bucket store.Bucket
}

type storedEntry struct {
	URL string `json:"url"`
}

// Open returns a List backed by the given store, under the bucket "entrylist_<name>".
func Open(s store.Store, name string) *List {
	return &List{bucket: s.Bucket("entrylist_" + name)}
}

// Add inserts or updates an entry in the list.
func (l *List) Add(title, url string) error {
	return l.bucket.Put(title, storedEntry{URL: url})
}

// Contains reports whether the list contains an entry with the given title.
func (l *List) Contains(title string) (bool, error) {
	var e storedEntry
	return l.bucket.Get(title, &e)
}

// Remove deletes an entry from the list.
func (l *List) Remove(title string) error {
	return l.bucket.Delete(title)
}

// Titles returns all entry titles in the list (order unspecified).
func (l *List) Titles() ([]string, error) {
	return l.bucket.Keys()
}
