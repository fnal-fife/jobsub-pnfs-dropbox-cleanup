package main

// Simple set implementation
type fileSet map[string]struct{}

func newFileSet(vals ...string) fileSet {
	f := make(fileSet)
	for _, v := range vals {
		f.add(v)
	}
	return f
}

func (f fileSet) add(file string) {
	f[file] = struct{}{}
}

func (f fileSet) remove(file string) {
	delete(f, file)
}

func (f fileSet) contains(file string) bool {
	_, ok := f[file]
	return ok
}

func (f fileSet) toSlice() []string {
	s := make([]string, 0, len(f))
	for k := range f {
		s = append(s, k)
	}
	return s
}

func (f fileSet) union(other fileSet) fileSet {
	u := newFileSet()
	for k := range f {
		u.add(k)
	}
	for k := range other {
		u.add(k)
	}
	return u
}

func (f fileSet) intersection(other fileSet) fileSet {
	i := newFileSet()
	for k := range f {
		if other.contains(k) {
			i.add(k)
		}
	}
	return i
}

func (f fileSet) difference(other fileSet) fileSet {
	d := newFileSet()
	for k := range f {
		if !other.contains(k) {
			d.add(k)
		}
	}
	return d
}
