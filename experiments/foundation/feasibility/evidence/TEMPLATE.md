# Feasibility evidence: <test name>

- **Issue:** #5 / #56
- **Status:** PASS | FAIL | UNTESTED
- **Date (UTC):**
- **Tester:** optional GitHub handle; do not include private account identifiers

## Test target

- OS / version:
- Architecture:
- Hardware model:
- CPU / memory (if resource evidence):
- Network interface(s): use generic names where actual names are not material
- Relevant USB VID:PID:
- Wi-Fi chipset / hardware revision:
- Driver / firmware / kernel:
- Tool versions (Go probe, tcpdump, Kismet, Docker, etc.):

## Security / scope

- Network is owned/authorized for testing: yes/no
- Management interface separated from Wi-Fi test radio: yes/no/not applicable
- Applicable security requirements from `docs/security/security-requirements.md`:
- Sensitive-data handling notes:

## Expected result

Describe the specific property being tested. Avoid vague goals such as "works".

## Procedure

List the exact sanitized commands/configuration needed to reproduce the result. Do not include credentials, private endpoints, household addresses, SSIDs, or packet payloads.

## Observed result

Record facts: process lifetime, observation count, directions seen, gap duration, driver state, channel list, failure text, resource measurements, etc.

## Evidence

Prefer sanitized excerpts, counts, hashes, screenshots with private details removed, and version output. Raw packet captures and household activity data should not be committed.

## Coverage conclusion

State exactly what this proves and what it does **not** prove.

Example: "This proves host-local capture on macOS 26.6.2 arm64 with interface en0 under the tested permission state. It does not prove visibility of traffic between other LAN devices."

## Cleanup / recovery

- Cleanup command(s):
- Previous interface/service state restored: yes/no
- Network connectivity verified after cleanup: yes/no
- Residual files/configuration:

## Follow-up

List failures, unsupported combinations, or questions that block promoting a Candidate support-matrix entry to Tested.
