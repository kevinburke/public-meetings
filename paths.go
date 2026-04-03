package main

import "path/filepath"

func artifactsDir(slug string) string {
	return filepath.Join(projectRoot(), "var", "artifacts", slug)
}

func agendaHTMLPath(m *Meeting) string {
	return filepath.Join(artifactsDir(m.InstanceSlug), m.ID+".html")
}

func annotationJSONPath(m *Meeting) string {
	return filepath.Join(artifactsDir(m.InstanceSlug), m.ID+"-annotations.json")
}

func annotateWorkDir(m *Meeting) string {
	return filepath.Join(projectRoot(), "var", "annotate", m.InstanceSlug, m.ID)
}
