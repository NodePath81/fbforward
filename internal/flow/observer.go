package flow

// Observer receives the canonical Flow lifecycle. Implementations must not
// block forwarding; slow persistence belongs behind an asynchronous queue.
type Observer interface {
	Open(Meta)
	Update(ID, Counters)
	Close(Summary)
	Reject(Rejection)
}

// NopObserver is useful when optional status, metrics, and audit projections
// are disabled.
type NopObserver struct{}

func (NopObserver) Open(Meta)           {}
func (NopObserver) Update(ID, Counters) {}
func (NopObserver) Close(Summary)       {}
func (NopObserver) Reject(Rejection)    {}

// MultiObserver fans one lifecycle event out to each enabled projection.
type MultiObserver []Observer

func (m MultiObserver) Open(meta Meta) {
	for _, observer := range m {
		if observer != nil {
			observer.Open(meta)
		}
	}
}

func (m MultiObserver) Update(id ID, counters Counters) {
	for _, observer := range m {
		if observer != nil {
			observer.Update(id, counters)
		}
	}
}

func (m MultiObserver) Close(summary Summary) {
	for _, observer := range m {
		if observer != nil {
			observer.Close(summary)
		}
	}
}

func (m MultiObserver) Reject(rejection Rejection) {
	for _, observer := range m {
		if observer != nil {
			observer.Reject(rejection)
		}
	}
}
