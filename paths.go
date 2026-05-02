package main

import "path/filepath"

func artifactsDir(slug string) string {
	return filepath.Join(projectRoot(), "var", "artifacts", slug)
}

func agendaHTMLPath(m *Meeting) string {
	return filepath.Join(artifactsDir(m.InstanceSlug), m.ID+".html")
}

// agendaPDFPath is the local cache for Highbond agenda PDFs. The annotator
// reads this and runs pdftohtml on it to recover the structured agenda items.
func agendaPDFPath(m *Meeting) string {
	return filepath.Join(artifactsDir(m.InstanceSlug), m.ID+".pdf")
}

func annotationJSONPath(m *Meeting) string {
	return filepath.Join(artifactsDir(m.InstanceSlug), m.ID+"-annotations.json")
}

func annotateWorkDir(m *Meeting) string {
	return filepath.Join(projectRoot(), "var", "annotate", m.InstanceSlug, m.ID)
}
