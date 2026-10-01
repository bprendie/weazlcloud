package library

// Copy the checkpoint candidate before changing it. Failed persistence must
// retain pending work so resume compares against durable catalog authority.
func cloneMetadataJob(job *PhotoMetadataJob) *PhotoMetadataJob {
	if job == nil {
		return nil
	}
	copy := *job
	copy.Entries = append([]PhotoMetadataEntry(nil), job.Entries...)
	return &copy
}

func (job *PhotoMetadataJob) hasPending() bool {
	for _, entry := range job.Entries {
		if entry.Status == "pending" {
			return true
		}
	}
	return false
}

func metadataSequence(job *PhotoMetadataJob) uint64 {
	if job == nil {
		return 0
	}
	return job.Sequence
}
