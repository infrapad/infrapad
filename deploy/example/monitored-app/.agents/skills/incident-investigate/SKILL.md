---
name: incident-investigate
description: Investigate an ongoing incident documented in infrapad. Given a document link/name, read the incident details, diagnose the problem, and write the investigation summary back to the document.
---

# Incident Investigation

Use this skill when asked to investigate an incident that has been recorded in an infrapad document.

Before proceeding, read the [infrapad skill](../infrapad/SKILL.md) to understand how infrapad documents and the CLI work.

## Investigation Steps

### 1. Pull the incident document

Given the document name (or ID), pull it to a local file:

```bash
infrapad md pull --document <document-name> --file incident.md
```

Read the file to understand:
- The **title** and **status** from the front matter.
- The **alerts_matcher** block — which alerts are firing, the `Since`/`Until` timestamps, and the `LabelsMatchers` to identify affected components.
- Any existing **markdown** blocks with prior notes.

### 2. Gather diagnostic information

Investigate the incident, gather evidence and attempt to find the root cause.

### 3. Write the investigation summary

Include the details about findings.

Append a new markdown block to the incident file with your findings:

```
::infrapad_block{type=markdown block=new}
# Investigation Summary

**Investigated at**: <current UTC timestamp>

## Findings

<Describe what you found — which endpoints are down/up, alert states, metrics, etc.>

## Diagnosis

<Your analysis of the root cause or current state>
```

### 4. Add another block with the proposed resolution.

DON'T TRY TO RESOLVE THE PROBLEM ON YOUR OWN. The user needs to approve
the request (and you don't have enough permissions to perform the actions).

Instead, add another block with recommended action.

```
::infrapad_block{type=markdown block=new}
## Recommended Actions

<What should be done to resolve or follow up>
```

IMPORTANT: the recommended actions part needs to be introduced
with a new `::inrapad_block`, as they will be treated differently later.

It's ok to add multiple blocks at once before pushing.

### 5. Push the summary to infrapad

Push the changes back to the server:

```bash
infrapad md push --file incident.md
```

The push command auto-detects changed and new blocks and pushes only those. This ensures the investigation is recorded in the shared incident document for the team.

## Important Notes

- Always **pull** the latest version before making changes to avoid conflicts.
- The investigation summary should be factual and based on observed data, not assumptions.
- Include timestamps in UTC format for consistency.
- Don't try to resolve the issue. Just wrap up with "the investigation has been finished".
