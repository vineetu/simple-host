package papermetrics

import (
	"math"
	"math/big"
	"math/bits"
	"sync"
	"sync/atomic"
)

const MetricDefVersion = 4
const LegacyBucketScheme = "legacy_16_upper_us_v1"
const BucketScheme = "exp2_scale3_us_v1"
const logBucketCount = 505 // zero bucket plus indices 0..503 (all positive int64 µs)

// Exact integer upper bounds avoid rounding errors near bucket boundaries
// above float64's 53-bit integer precision. Three integer square roots yield
// floor(2^(i/8)); this setup runs once, never on a request.
var logBucketUpper = func() [logBucketCount]int64 {
	var result [logBucketCount]int64
	result[0] = 1
	for i := 1; i < logBucketCount; i++ {
		value := new(big.Int).Lsh(big.NewInt(1), uint(i))
		for j := 0; j < 3; j++ {
			value.Sqrt(value)
		}
		if value.IsInt64() {
			result[i] = value.Int64()
		} else {
			result[i] = math.MaxInt64
		}
	}
	return result
}()

// LogHistogram is a mergeable scale-3 exponential histogram in microseconds.
// Observe is concurrent; Snapshot must be taken after writers have quiesced.
// The Host collector deliberately continues to use its original ClassHist.
type LogHistogram struct {
	chunks          [16]atomic.Pointer[histogramChunk]
	n, sumUS, maxUS atomic.Int64
}

// Histogram is the sparse, contiguous nonempty span stored in INT8[]. Index -1
// is the <=1µs bucket. Positive values use ceil(log2(us)*8)-1. Zeros inside the
// span are retained so offsets are sufficient to merge without sample data.
type Histogram struct {
	Offset          int16
	Counts          []int64
	N, SumUS, MaxUS int64
}

func logBucket(us int64) int {
	if us <= 1 {
		return 0
	}
	base := (bits.Len64(uint64(us)) - 1) * 8
	if us&(us-1) == 0 {
		return base
	}
	lo, hi := base+1, base+8
	for lo < hi {
		mid := (lo + hi) / 2
		if us <= logBucketUpper[mid] {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

func AtomicMax(dst *atomic.Int64, value int64) {
	for old := dst.Load(); value > old; old = dst.Load() {
		if dst.CompareAndSwap(old, value) {
			return
		}
	}
}

func (h *LogHistogram) Observe(us int64) {
	if us < 0 {
		us = 0
	}
	h.bucket(logBucket(us)).Add(1)
	h.n.Add(1)
	h.sumUS.Add(us)
	AtomicMax(&h.maxUS, us)
}

func (h *LogHistogram) Snapshot() Histogram {
	s := Histogram{Offset: -1, N: h.n.Load(), SumUS: h.sumUS.Load(), MaxUS: h.maxUS.Load(), Counts: []int64{}}
	first, last := -1, -1
	for chunkIndex := range h.chunks {
		chunk := h.chunks[chunkIndex].Load()
		if chunk == nil {
			continue
		}
		for j := range chunk {
			i := chunkIndex*32 + j
			if i >= logBucketCount {
				break
			}
			if chunk[j].Load() > 0 {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
	}
	if first < 0 {
		return s
	}
	s.Offset = int16(first - 1)
	s.Counts = make([]int64, last-first+1)
	for i := range s.Counts {
		index := first + i
		if chunk := h.chunks[index/32].Load(); chunk != nil {
			s.Counts[i] = chunk[index%32].Load()
		}
	}

	return s
}

// MergeHistograms sums counts and exact moments; no percentile is ever merged.
func MergeHistograms(histograms ...Histogram) Histogram {
	result := Histogram{Offset: -1, Counts: []int64{}}
	first, last := int16(504), int16(-2)
	for _, h := range histograms {
		result.N += h.N
		result.SumUS += h.SumUS
		if h.MaxUS > result.MaxUS {
			result.MaxUS = h.MaxUS
		}
		if len(h.Counts) > 0 {
			if h.Offset < first {
				first = h.Offset
			}
			end := h.Offset + int16(len(h.Counts)) - 1
			if end > last {
				last = end
			}
		}
	}
	if last < first {
		return result
	}
	result.Offset = first
	result.Counts = make([]int64, int(last-first)+1)
	for _, h := range histograms {
		for i, n := range h.Counts {
			result.Counts[int(h.Offset-first)+i] += n
		}
	}
	return result
}

// Quantile estimates a nearest-rank quantile using the bucket's harmonic
// midpoint, with <=4.33% relative error for values >1µs. The zero bucket has
// <=1µs absolute error. Callers must report n and max and require n>=1000 for
// p99 or n>=10000 for p99.9; this method does not manufacture extra samples.
func (h Histogram) Quantile(q float64) float64 {
	if h.N == 0 {
		return 0
	}
	rank := int64(math.Ceil(q * float64(h.N)))
	if rank < 1 {
		rank = 1
	}
	var seen int64
	for i, n := range h.Counts {
		seen += n
		if seen < rank {
			continue
		}
		index := int(h.Offset) + i
		if index < 0 {
			if h.MaxUS == 0 {
				return 0
			}
			return 1
		}
		lo, hi := math.Exp2(float64(index)/8), math.Exp2(float64(index+1)/8)
		return 2 * lo * hi / (lo + hi)
	}
	return float64(h.MaxUS)
}

func (h *LogHistogram) Merge(other Histogram) {
	for i, n := range other.Counts {
		if n != 0 {
			h.bucket(int(other.Offset) + i + 1).Add(n)
		}
	}
	h.n.Add(other.N)
	h.sumUS.Add(other.SumUS)
	AtomicMax(&h.maxUS, other.MaxUS)
}

type histogramChunk [32]atomic.Int64

var histogramChunks sync.Pool

func (h *LogHistogram) bucket(index int) *atomic.Int64 {
	ptr := &h.chunks[index/32]
	chunk := ptr.Load()
	if chunk == nil {
		chunk, _ = histogramChunks.Get().(*histogramChunk)
		if chunk == nil {
			chunk = &histogramChunk{}
		}
		if !ptr.CompareAndSwap(nil, chunk) {
			histogramChunks.Put(chunk)
			chunk = ptr.Load()
		}
	}
	return &chunk[index%32]
}

// Reset requires quiescence. Sparse chunks are zeroed and reused by the pool.
func (h *LogHistogram) Reset() {
	for i := range h.chunks {
		if chunk := h.chunks[i].Swap(nil); chunk != nil {
			for j := range chunk {
				chunk[j].Store(0)
			}
			histogramChunks.Put(chunk)
		}
	}
	h.n.Store(0)
	h.sumUS.Store(0)
	h.maxUS.Store(0)
}

func (h *LogHistogram) Count() int64 { return h.n.Load() }
