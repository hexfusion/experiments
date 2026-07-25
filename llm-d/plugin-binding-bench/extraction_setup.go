package main

// SetupExtraction installs the shared indexer and warms it to a chosen prefix
// depth. Depth is the axis that actually varies per request in production: the
// producer breaks at the first uncached block, so a cold cache costs one lookup
// and a hot one costs as many lookups as the prompt has matched blocks.
func SetupExtraction(sample []byte, cacheDepthPct, serversPerBlock, endpoints int) error {
	base, err := parseBody(sample)
	if err != nil {
		return err
	}
	ix := NewIndexer()
	depth := len(base.BlockKeys) * cacheDepthPct / 100
	ix.Warm(base.BlockKeys, depth, serversPerBlock, endpoints)
	globalExtractor = NewExtractor(ix)
	return nil
}
