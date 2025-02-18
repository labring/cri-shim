package types

type CommitStatus int

const (
	NoneCommit CommitStatus = iota
	StopCommit
	RemoveCommit
	StatusCommit
	ErrorCommit
	ErrorPush
)
