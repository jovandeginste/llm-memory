package embed

import (
	"context"
	"math"
)

// Hash is a dependency-free lexical embedder. It hashes unigrams and bigrams
// into a fixed number of signed buckets and L2-normalizes the result, so
// cosine similarity approximates token overlap. It is the offline fallback and
// is deterministic across processes and machines.
type Hash struct {
	dims int
}

// NewHash builds a hash embedder with the given dimension.
func NewHash(dims int) *Hash {
	if dims <= 0 {
		dims = 256
	}
	return &Hash{dims: dims}
}

// Name implements Embedder.
func (h *Hash) Name() string { return "hash-" + itoa(h.dims) }

// Embed implements Embedder.
func (h *Hash) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = h.vector(text)
	}
	return out, nil
}

// vector builds the signed hashed bag-of-words for one text.
func (h *Hash) vector(text string) []float32 {
	vec := make([]float32, h.dims)
	tokens := Tokenize(text)
	add := func(tok string, weight float32) {
		sum := uint64(1469598103934665603)
		for i := 0; i < len(tok); i++ {
			sum ^= uint64(tok[i])
			sum *= 1099511628211
		}
		idx := int(sum % uint64(h.dims))
		if sum&(1<<63) != 0 {
			vec[idx] -= weight
		} else {
			vec[idx] += weight
		}
	}
	for i, tok := range tokens {
		add(tok, 1)
		if i+1 < len(tokens) {
			add(tok+"\x00"+tokens[i+1], 0.5)
		}
	}
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return vec
	}
	scale := float32(1 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= scale
	}
	return vec
}

// itoa is a tiny strconv.Itoa to keep the package imports small.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
