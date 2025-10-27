package obs

import "runtime/metrics"

// cpuReader okuduğumuz sınıfları toplar (idle hariç)
type cpuReader struct {
	keys    []string
	samples []metrics.Sample
}

func newCPUReader() *cpuReader {
	keys := []string{
		"/cpu/classes/user:cpu-seconds",
		"/cpu/classes/gomisc:cpu-seconds",
		"/cpu/classes/scavenge:cpu-seconds",
		"/cpu/classes/gc/mark/assist:cpu-seconds",
		"/cpu/classes/gc/mark/dedicated:cpu-seconds",
		"/cpu/classes/gc/pause:cpu-seconds",
	}
	samples := make([]metrics.Sample, len(keys))
	for i, k := range keys {
		samples[i].Name = k
	}
	return &cpuReader{keys: keys, samples: samples}
}

func (r *cpuReader) Read() float64 {
	metrics.Read(r.samples)
	total := 0.0
	for _, s := range r.samples {
		if s.Value.Kind() == metrics.KindFloat64 {
			total += s.Value.Float64()
		}
	}
	return total
}
