// Copyright (c) 2026 hoardDB.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package bson

// CheckBuiltLength exposes the builder's int32 length check to the external
// tests: a 2 GiB document is too large to build in a test.
var CheckBuiltLength = checkBuiltLength

// EntryPointPlanCaches returns the package's plan cache and the caches its
// encoder and decoder use, for TestEntryPointsShareOnePlanCache.
func EntryPointPlanCaches() (declared, encoderCache, decoderCache any) {
	return planCache, libraryEncoder.Cache(), libraryDecoder.Cache()
}
