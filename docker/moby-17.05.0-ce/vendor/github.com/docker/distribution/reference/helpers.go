package reference

import "path"

// IsNameOnly returns true if reference only contains a repo name.
func IsNameOnly(ref Named) bool {
	if _, ok := ref.(NamedTagged); ok {
		return false
	}
	if _, ok := ref.(Canonical); ok {
		return false
	}
	return true
}

// FamiliarName returns the familiar name string
// for the given named, familiarizing if needed.
//harbor.intra.xxxx.com/xxxx/centos:XXX 去掉:后面的tag，变为harbor.intra.xxxx.com/xxxx/centos
func FamiliarName(ref Named) string {
	if nn, ok := ref.(normalizedNamed); ok {
		return nn.Familiar().Name()
	}
	return ref.Name()
}

// FamiliarString returns the familiar string representation
// for the given reference, familiarizing if needed.
func FamiliarString(ref Reference) string {
	if nn, ok := ref.(normalizedNamed); ok {
		return nn.Familiar().String()
	}
	return ref.String()
}

// FamiliarMatch reports whether ref matches the specified pattern.
// See https://godoc.org/path#Match for supported patterns.
func FamiliarMatch(pattern string, ref Reference) (bool, error) {
	matched, err := path.Match(pattern, FamiliarString(ref))
	if namedRef, isNamed := ref.(Named); isNamed && !matched {
		matched, _ = path.Match(pattern, FamiliarName(namedRef))
	}
	return matched, err
}
