package diagnostic

import "github.com/ChristopherAparicio/aisync/internal/session"

func init() { RegisterModule(&ImagesModule{}) }

// ImagesModule activates when the session contains screenshots, simulator captures,
// or inline images. It runs image-specific detectors.
type ImagesModule struct{}

func (m *ImagesModule) Name() string { return "images" }

// ShouldActivate returns true if the session has inline images, screenshot
// tool calls (simctl/sips), or image file reads.
func (m *ImagesModule) ShouldActivate(sess *session.Session) bool {
	return SessionHasImages(sess)
}

// Detect runs the image-specific detectors.
func (m *ImagesModule) Detect(r *InspectReport, _ *session.Session) []Problem {
	var problems []Problem

	problems = append(problems, detectExpensiveScreenshots(r)...)
	problems = append(problems, detectOversizedScreenshots(r)...)
	problems = append(problems, detectUnresizedScreenshots(r)...)

	return problems
}
