package workflow

// ManagedHostStatusWriteAllowed decides whether a managed run may write its
// host issue's status.
//
// One issue can carry several managed runs across its life, and each of them
// maintains the issue as though it were alone. That is not merely untidy — it
// oscillates. A completed run is reconciled whenever the host is not 'done',
// and a running one whenever the host is not 'in_progress', so starting a
// second run on an issue whose first run finished leaves the two rewriting the
// same field forever, each correct about itself.
//
// Authority therefore belongs to the run that is still live. A finished run
// may close the host only when nothing else is working on it — including the
// case where the finished run is itself the one that was live, which is how an
// ordinary completion still lands.
func ManagedHostStatusWriteAllowed(liveRunExists, writerIsLive bool) bool {
	if writerIsLive {
		return true
	}
	return !liveRunExists
}
