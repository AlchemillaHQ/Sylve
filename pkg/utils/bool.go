package utils

func PtrToBool(b *bool) bool {
	if b == nil {
		return false
	}

	return *b
}
