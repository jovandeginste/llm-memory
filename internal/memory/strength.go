package memory

import (
	"math"
	"time"

	"llm-memory/internal/store"
)

// Strength tuning. The formula is deliberately simple and documented in the
// README: importance dominates, age and access recency pull it down, links and
// category raise it.
const (
	halfLifeDays     = 30.0 // age decay: half the age component per month
	accessHalfLife   = 7.0  // access recency: half the recency component per week
	linkBonusPerLink = 0.05 // each link adds strength...
	linkBonusMax     = 0.30 // ...up to this cap
	archiveThreshold = 0.15 // below this, decay archives a memory
	purgeThreshold   = 0.05 // below this, decay may purge a memory
	importanceWeight = 0.55
	ageWeight        = 0.30
	accessWeight     = 0.15
)

// categoryWeight makes decisions and preferences stickier than raw facts.
var categoryWeight = map[string]float64{
	"decision":   1.3,
	"preference": 1.4,
}

// Strength computes the 0..1 strength of a memory from its importance, age,
// last access, link count, and category.
func Strength(m *store.Memory, degree int, now time.Time) float64 {
	ageDays := now.Sub(m.CreatedAt).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	accessDays := now.Sub(m.LastAccess).Hours() / 24
	if accessDays < 0 {
		accessDays = 0
	}
	age := math.Pow(0.5, ageDays/halfLifeDays)
	recency := math.Pow(0.5, accessDays/accessHalfLife)
	linkBonus := math.Min(linkBonusMax, linkBonusPerLink*float64(degree))
	weight := 1.0
	if w, ok := categoryWeight[m.Category]; ok {
		weight = w
	}
	strength := (importanceWeight*m.Importance + ageWeight*age + accessWeight*recency) * weight
	strength += linkBonus
	return clamp01(strength)
}

// clamp01 bounds a value to [0, 1].
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
