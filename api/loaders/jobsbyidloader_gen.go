// Code generated by github.com/vektah/dataloaden, DO NOT EDIT.

package loaders

import (
	"sync"
	"time"

	"git.sr.ht/~sircmpwn/builds.sr.ht/api/graph/model"
)

// JobsByIDLoaderConfig captures the config to create a new JobsByIDLoader
type JobsByIDLoaderConfig struct {
	// Fetch is a method that provides the data for the loader
	Fetch func(keys []int) ([]*model.Job, []error)

	// Wait is how long wait before sending a batch
	Wait time.Duration

	// MaxBatch will limit the maximum number of keys to send in one batch, 0 = not limit
	MaxBatch int
}

// NewJobsByIDLoader creates a new JobsByIDLoader given a fetch, wait, and maxBatch
func NewJobsByIDLoader(config JobsByIDLoaderConfig) *JobsByIDLoader {
	return &JobsByIDLoader{
		fetch:    config.Fetch,
		wait:     config.Wait,
		maxBatch: config.MaxBatch,
	}
}

// JobsByIDLoader batches and caches requests
type JobsByIDLoader struct {
	// this method provides the data for the loader
	fetch func(keys []int) ([]*model.Job, []error)

	// how long to done before sending a batch
	wait time.Duration

	// this will limit the maximum number of keys to send in one batch, 0 = no limit
	maxBatch int

	// INTERNAL

	// lazily created cache
	cache map[int]*model.Job

	// the current batch. keys will continue to be collected until timeout is hit,
	// then everything will be sent to the fetch method and out to the listeners
	batch *jobsByIDLoaderBatch

	// mutex to prevent races
	mu sync.Mutex
}

type jobsByIDLoaderBatch struct {
	keys    []int
	data    []*model.Job
	error   []error
	closing bool
	done    chan struct{}
}

// Load a Job by key, batching and caching will be applied automatically
func (l *JobsByIDLoader) Load(key int) (*model.Job, error) {
	return l.LoadThunk(key)()
}

// LoadThunk returns a function that when called will block waiting for a Job.
// This method should be used if you want one goroutine to make requests to many
// different data loaders without blocking until the thunk is called.
func (l *JobsByIDLoader) LoadThunk(key int) func() (*model.Job, error) {
	l.mu.Lock()
	if it, ok := l.cache[key]; ok {
		l.mu.Unlock()
		return func() (*model.Job, error) {
			return it, nil
		}
	}
	if l.batch == nil {
		l.batch = &jobsByIDLoaderBatch{done: make(chan struct{})}
	}
	batch := l.batch
	pos := batch.keyIndex(l, key)
	l.mu.Unlock()

	return func() (*model.Job, error) {
		<-batch.done

		var data *model.Job
		if pos < len(batch.data) {
			data = batch.data[pos]
		}

		var err error
		// its convenient to be able to return a single error for everything
		if len(batch.error) == 1 {
			err = batch.error[0]
		} else if batch.error != nil {
			err = batch.error[pos]
		}

		if err == nil {
			l.mu.Lock()
			l.unsafeSet(key, data)
			l.mu.Unlock()
		}

		return data, err
	}
}

// LoadAll fetches many keys at once. It will be broken into appropriate sized
// sub batches depending on how the loader is configured
func (l *JobsByIDLoader) LoadAll(keys []int) ([]*model.Job, []error) {
	results := make([]func() (*model.Job, error), len(keys))

	for i, key := range keys {
		results[i] = l.LoadThunk(key)
	}

	jobs := make([]*model.Job, len(keys))
	errors := make([]error, len(keys))
	for i, thunk := range results {
		jobs[i], errors[i] = thunk()
	}
	return jobs, errors
}

// LoadAllThunk returns a function that when called will block waiting for a Jobs.
// This method should be used if you want one goroutine to make requests to many
// different data loaders without blocking until the thunk is called.
func (l *JobsByIDLoader) LoadAllThunk(keys []int) func() ([]*model.Job, []error) {
	results := make([]func() (*model.Job, error), len(keys))
	for i, key := range keys {
		results[i] = l.LoadThunk(key)
	}
	return func() ([]*model.Job, []error) {
		jobs := make([]*model.Job, len(keys))
		errors := make([]error, len(keys))
		for i, thunk := range results {
			jobs[i], errors[i] = thunk()
		}
		return jobs, errors
	}
}

// Prime the cache with the provided key and value. If the key already exists, no change is made
// and false is returned.
// (To forcefully prime the cache, clear the key first with loader.clear(key).prime(key, value).)
func (l *JobsByIDLoader) Prime(key int, value *model.Job) bool {
	l.mu.Lock()
	var found bool
	if _, found = l.cache[key]; !found {
		// make a copy when writing to the cache, its easy to pass a pointer in from a loop var
		// and end up with the whole cache pointing to the same value.
		cpy := *value
		l.unsafeSet(key, &cpy)
	}
	l.mu.Unlock()
	return !found
}

// Clear the value at key from the cache, if it exists
func (l *JobsByIDLoader) Clear(key int) {
	l.mu.Lock()
	delete(l.cache, key)
	l.mu.Unlock()
}

func (l *JobsByIDLoader) unsafeSet(key int, value *model.Job) {
	if l.cache == nil {
		l.cache = map[int]*model.Job{}
	}
	l.cache[key] = value
}

// keyIndex will return the location of the key in the batch, if its not found
// it will add the key to the batch
func (b *jobsByIDLoaderBatch) keyIndex(l *JobsByIDLoader, key int) int {
	for i, existingKey := range b.keys {
		if key == existingKey {
			return i
		}
	}

	pos := len(b.keys)
	b.keys = append(b.keys, key)
	if pos == 0 {
		go b.startTimer(l)
	}

	if l.maxBatch != 0 && pos >= l.maxBatch-1 {
		if !b.closing {
			b.closing = true
			l.batch = nil
			go b.end(l)
		}
	}

	return pos
}

func (b *jobsByIDLoaderBatch) startTimer(l *JobsByIDLoader) {
	time.Sleep(l.wait)
	l.mu.Lock()

	// we must have hit a batch limit and are already finalizing this batch
	if b.closing {
		l.mu.Unlock()
		return
	}

	l.batch = nil
	l.mu.Unlock()

	b.end(l)
}

func (b *jobsByIDLoaderBatch) end(l *JobsByIDLoader) {
	b.data, b.error = l.fetch(b.keys)
	close(b.done)
}
