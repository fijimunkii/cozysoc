package storage

// Test-only bridge lets the external storage tests use the real Device Watch
// producer without an import cycle or an exported production activation API.
const EvidenceBatchSchemaForTest = evidenceBatchSchema

var AppendEvidenceBatchForTest = appendEvidenceBatch
var ValidateEvidenceBatchLookupsForTest = validateEvidenceBatchLookups

var NextEvidenceBatchExpiryForTest = nextEvidenceBatchExpiry

var ValidateEvidenceBatchIdentityGroupForTest = validateEvidenceBatchIdentityGroup
var ValidateEvidenceBatchClaimBoundsForTest = validateEvidenceBatchClaimBounds
