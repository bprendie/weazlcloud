package photos

import (
	"fmt"
	"testing"
	"time"
)

// No image bytes or uploads: isolate queue costs at the observed library size.
func BenchmarkMediaQueueDispatch(b *testing.B) {
	for _, count := range []int{1000, 37082} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			jobs := make([]MediaJob, count)
			for i := range jobs {
				jobs[i] = NewMediaJob("owner", fmt.Sprint(i), 1, "thumbnail:320", "media-v4", 3)
			}
			q := JobQueue{Jobs: jobs}
			q.Reindex()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				leased := q.Lease("bench", time.Now(), time.Minute, 4)
				for _, job := range leased {
					q.Release(job.ID, "bench")
				}
			}
		})
	}
}

func BenchmarkMediaQueueBuild37082(b *testing.B) {
	jobs := make([]MediaJob, 37082)
	for i := range jobs {
		jobs[i] = NewMediaJob("owner", fmt.Sprint(i), 1, "thumbnail:320", "media-v4", 3)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := JobQueue{}
		for _, job := range jobs {
			q.Upsert(job)
		}
	}
}
