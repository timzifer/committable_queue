package queue

type DropPolicy int

const (
	DropOldest DropPolicy = iota
	DropNewest
)

type Options struct {
	MaxLen     int
	DropPolicy DropPolicy
}

func defaultOptions() Options {
	return Options{
		DropPolicy: DropOldest,
	}
}
