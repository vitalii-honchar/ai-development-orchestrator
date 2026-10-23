package pointers

type model struct {
	count int
}

func Mutate(source *model, number *int) {
	source.count++
	alias := source
	alias.count++
	value := *number
	*number = value + 1
}
