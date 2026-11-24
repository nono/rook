// Package github reads open pull requests from the GitHub GraphQL API.
package github

import "time"

// Actor is a GitHub account.
type Actor struct {
	Login     string
	AvatarURL string
	// Bot is true for each account that is not a GraphQL User: a bot, a
	// mannequin, or a deleted account (Login "ghost").
	Bot bool
}

// Review is a submitted review of a pull request.
type Review struct {
	Author Actor
	// State is APPROVED, CHANGES_REQUESTED, COMMENTED or DISMISSED.
	// The client removes PENDING reviews.
	State       string
	SubmittedAt time.Time
	CommitOID   string
}

// PR is an open pull request.
type PR struct {
	Repo        string // "owner/name", as configured
	CurrentRepo string // "owner/name" after a rename (GitHub follows renames), else Repo
	Number      int
	Title       string
	URL         string
	IsDraft     bool
	UpdatedAt   time.Time
	HeadRefName string
	HeadOID     string
	Fork        bool // the head branch is in another repo
	// Mergeable is MERGEABLE, CONFLICTING or UNKNOWN.
	Mergeable      string
	Author         Actor
	RequestedUsers []Actor
	RequestedTeams []string // team slugs
	Reviews        []Review // any order
	// CIState is the statusCheckRollup state of the head commit, "" when there are no checks.
	CIState string
}
