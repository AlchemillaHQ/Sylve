package utils

func UniqueAppend(dst []string, src []string) []string {
	set := make(map[string]struct{}, len(dst))
	for _, v := range dst {
		set[v] = struct{}{}
	}
	for _, v := range src {
		if _, ok := set[v]; !ok {
			dst = append(dst, v)
		}
	}
	return dst
}

func Unique(input []string) []string {
	set := make(map[string]struct{}, len(input))
	var result []string
	for _, v := range input {
		if _, ok := set[v]; !ok {
			set[v] = struct{}{}
			result = append(result, v)
		}
	}
	return result
}
