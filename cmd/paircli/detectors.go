package main

// Detector packages register themselves in init(). Keep this list complete.
import (
	_ "github.com/rutvikchandla3/paircli/internal/detect/authorship"
	_ "github.com/rutvikchandla3/paircli/internal/detect/consistency"
	_ "github.com/rutvikchandla3/paircli/internal/detect/decisions"
	_ "github.com/rutvikchandla3/paircli/internal/detect/exposure"
	_ "github.com/rutvikchandla3/paircli/internal/detect/friction"
	_ "github.com/rutvikchandla3/paircli/internal/detect/intent"
	_ "github.com/rutvikchandla3/paircli/internal/detect/oversight"
	_ "github.com/rutvikchandla3/paircli/internal/detect/verification"
)
