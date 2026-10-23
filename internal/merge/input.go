package merge

// Input is one per-query CodeQL output file plus the project whose database
// produced it. Project is empty for the flat single-project layout.
type Input struct {
	Path    string
	Project string
}
