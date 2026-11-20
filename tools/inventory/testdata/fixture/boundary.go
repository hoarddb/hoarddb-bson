// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package fixture

func checkDocumentDepth(encoded []byte, limit int) error { return nil }

func refuseDeepRequest(encoded []byte) error { return checkDocumentDepth(encoded, 101) }

// Server stands in for the receiver of the server's boundary methods.
type Server struct{}

func (server *Server) restoreChunk(encoded []byte) error {
	return checkDocumentDepth(encoded, 100)
}

func replayWAL(encoded []byte) {
	replayOne := func() error { return checkDocumentDepth(encoded, 102) }
	_ = replayOne()
}

var _ = checkDocumentDepth
