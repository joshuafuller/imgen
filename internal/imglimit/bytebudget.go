package imglimit

import (
	"context"
	"fmt"
	"sync"
)

// ByteBudget is a byte allowance that a caller shares with the code that writes
// files. Take is called right before each write, so the cap holds however large
// post-processing made the output.
type ByteBudget struct {
	mu   sync.Mutex
	left int64
	used int64
}

// NewByteBudget returns a budget of n bytes.
func NewByteBudget(n int64) *ByteBudget { return &ByteBudget{left: n} }

// Take spends n bytes, or fails with ErrTooBig and spends nothing.
func (b *ByteBudget) Take(n int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.left {
		return fmt.Errorf("%w: writing %d bytes would pass the output byte allowance (%d left)", ErrTooBig, n, b.left)
	}
	b.left -= n
	b.used += n
	return nil
}

// Used reports the bytes spent so far.
func (b *ByteBudget) Used() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

type budgetKey struct{}

// WithByteBudget returns a context that carries b.
func WithByteBudget(ctx context.Context, b *ByteBudget) context.Context {
	return context.WithValue(ctx, budgetKey{}, b)
}

// ByteBudgetFrom returns the budget carried by ctx, or nil.
func ByteBudgetFrom(ctx context.Context) *ByteBudget {
	b, _ := ctx.Value(budgetKey{}).(*ByteBudget)
	return b
}
