package model

import "time"

type Entry struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	PickCount int       `json:"pick_count"`
	Excluded  bool      `json:"excluded"`
	Tags      []string  `json:"tags,omitempty"`
	TagIDs    []int     `json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Tag struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type EntryTag struct {
	EntryID int
	TagID   int
}

type Pick struct {
	ID         int       `json:"id"`
	EntryID    int       `json:"entry_id"`
	EntryName  string    `json:"entry_name"`
	PickNumber int       `json:"pick_number"`
	PickedAt   time.Time `json:"picked_at"`
}

type DrawRequest struct {
	MustHave []string `json:"must_have"`
	AnyOf    []string `json:"any_of"`
}

type DrawResult struct {
	Winner       Entry    `json:"winner"`
	PickID       int      `json:"pick_id"`
	PickNumber   int      `json:"pick_number"`
	MustHave     []string `json:"must_have,omitempty"`
	AnyOf        []string `json:"any_of,omitempty"`
	EligiblePool int      `json:"eligible_pool"`
}

// List is the named set of Entries a raffle draws from, as it crosses between
// the List exchange (import/export), the handlers and the store.
// An empty Name means "no name supplied": importing it keeps the current name.
type List struct {
	Name    string
	Entries []ListEntry
}

// ListEntry is one Entry of a List. Nil PickCount/Excluded and a nil Tags
// slice mean "not supplied": merging leaves the existing value as is.
type ListEntry struct {
	Name      string
	PickCount *int
	Excluded  *bool
	Tags      []string
}
