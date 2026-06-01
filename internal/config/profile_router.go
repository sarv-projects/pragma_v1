package config

import "strings"

// SelectProfile picks the best build profile based on keywords in the user's
// project description. Defaults to FastAPI + SQLite when no clear match.
func SelectProfile(description string) string {
	d := strings.ToLower(description)

	// Explicit framework / runtime references
	hasFrontend := containsAny(d, []string{"react", "nextjs", "next.js", "frontend", "ui", "page", "dashboard", "component", "landing", " vue", "svelte", "angular"})
	hasGo := containsAny(d, []string{" go", "golang", "fiber"})
	hasNode := containsAny(d, []string{" node", "nodejs", "node.js", "express", "javascript", "typescript"})
	hasHono := containsAny(d, []string{"hono"})
	hasPrisma := containsAny(d, []string{"prisma"})

	// Backend-only explicit opt-out of frontend profiles
	backendOnly := containsAny(d, []string{"backend only", "api only", "backend-only", "api-only", "no frontend", "no ui", "rest api", "restful api"})

	// Database references
	hasPostgres := containsAny(d, []string{"postgres", "postgresql", "psql"})

	// Decision order: explicit runtime > frontend-default > generic-default
	switch {
	case hasHono:
		if hasPostgres {
			return "hono-drizzle"
		}
		return "hono-drizzle-sqlite"
	case hasFrontend && !backendOnly:
		// If the user mentions a frontend framework or UI concepts,
		// default to Next.js (catches node+react, react-only, vague-UI, etc.)
		return "nextjs-app"
	case hasGo:
		if hasPostgres {
			return "fiber-sqlc"
		}
		return "fiber-sqlc-sqlite"
	case hasNode:
		if hasPrisma {
			if hasPostgres {
				return "express-prisma"
			}
			return "express-prisma-sqlite"
		}
		if hasPostgres {
			return "express-drizzle"
		}
		return "express-drizzle-sqlite"
	}

	// Default: Python FastAPI — lowest friction, most accessible
	if hasPostgres {
		return "fastapi-async"
	}
	return "fastapi-async-sqlite"
}

func containsAny(s string, keys []string) bool {
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
