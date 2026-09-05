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

// EntryImport carries optional full state for import (nil = leave as-is).
type EntryImport struct {
	Name      string
	PickCount *int
	Excluded  *bool
	Tags      []string
}
