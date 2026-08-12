# Infra book

A system for keeping track and making sense of changes in the kubernetes environment.

The goal of the system is to capture and provide human-comprehnesible information
about events in the cluster.

The idea is to keep a set of journals that represent the top-level object
to track in the system (an incident, interesting monitoring signal observation,
perforamcne analysis), that should include everything relevant to the particular
situation.

A journal consists of a set of records of different types (markdown notes, 
alerts links, external links). The record is an atomic unit of piece of information
in the journal.

Data types:

```
Journal:
  jouranl_id: uuid
  type: Enum["Incident", "Observation", "Analysis"]
  title: string
  records: Record[]
  
Record:
  record_id: uuid
  author: uuid
  timestamp: uuid
  type: Enum["Markdown", "AlertsLink", "Comment", "K8sResourcesLink"]
  content: any # depends on  type
```

Given the journal is meant for colaboration of multiple actors (human and AI agents),
it's curcial for it to be able keep a history of the changes on per-record basis,
so that it's easy to see the who edited what and save doing changes.

Non-functional requires:

Backend:
  - go-lang
  
Frontend:
  - typescript
