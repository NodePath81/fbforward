package util

// BoolValue returns the value of a *bool pointer, or the fallback if nil.
func BoolValue(ptr *bool, fallback bool) bool {
	if ptr == nil {
		return fallback
	}
	return *ptr
}
