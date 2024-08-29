package container

type Info struct {
	ID          string
	CommitImage string

	CommitEnabled bool
	PushEnabled   bool
	SquashEnabled bool
}
