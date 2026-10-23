package copies

type model struct {
	count int
}

func Copy(source *model) {
	local := *source
	var assigned model
	assigned = *source
	var inferred = *source
	var typed model = *source
	target := &model{}
	*target = local
	_, _, _, _ = local, assigned, inferred, typed
}
