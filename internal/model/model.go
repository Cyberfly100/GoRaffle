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

// TagFilter is the Tags that limit a Draw: every Must-have Tag is required,
// and of the any-of Tags an Entry needs at least one. An empty TagFilter
// limits nothing.
type TagFilter struct {
	MustHave []string `json:"must_have,omitempty"`
	AnyOf    []string `json:"any_of,omitempty"`
}

// DrawRequest is the body of a Draw: its Tag filter.
type DrawRequest struct {
	TagFilter
}

// List is the named set of Entries a raffle draws from, as it crosses between
// the List exchange (import/export), the handlers and the store.
// An empty Name means "no name supplied": an Import then resets the List
// name to "Entries".
type List struct {
	Name    string
	Entries []ListEntry
	Picks   []ListPick
}

// ListEntry is one Entry of a List. Nil PickCount means "not supplied": an
// Import then sets it to the number of the Entry's Picks in the file.
// Nil Excluded means false. A nil Tags slice means no Tags.
type ListEntry struct {
	Name      string
	PickCount *int
	Excluded  *bool
	Tags      []string
}

// ListPick is one Pick of a List's History as it crosses between the List
// exchange, the handlers and the store: the Entry's name, the time it was
// picked and its Pick number.
type ListPick struct {
	EntryName  string
	PickedAt   time.Time
	PickNumber int
}
