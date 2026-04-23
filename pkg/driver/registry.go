package driver

import (
	"fmt"
	"sort"
	"sync"
)

// Registry is a thread-safe in-process registry of named MigrationDrivers.
type Registry struct {
	mu      sync.RWMutex
	drivers map[string]Driver
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{drivers: make(map[string]Driver)}
}

// Register adds a driver to the registry. Returns an error if a driver with
// the same name is already registered.
func (r *Registry) Register(d Driver) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := d.Name()
	if _, exists := r.drivers[name]; exists {
		return fmt.Errorf("driver %q already registered", name)
	}
	r.drivers[name] = d
	return nil
}

// MustRegister is like Register but panics on error.
func (r *Registry) MustRegister(d Driver) {
	if err := r.Register(d); err != nil {
		panic(err)
	}
}

// Get returns the driver with the given name, or an error if not found.
func (r *Registry) Get(name string) (Driver, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.drivers[name]
	if !ok {
		return nil, fmt.Errorf("driver %q not found; registered: %v", name, r.names())
	}
	return d, nil
}

// Names returns the sorted list of registered driver names.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.names()
}

func (r *Registry) names() []string {
	names := make([]string, 0, len(r.drivers))
	for n := range r.drivers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
