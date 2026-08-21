# InfraPad

Trusted notes about activities in the infrastructure your infrastructure.

## The problem

The more components there are in the infrastructure, the more signal gets produced.
Without a good way of organizing the information, it's easy to get lost. While
many tools exist to address a particular use-case, the tools often don't integrate
with each other, contributing even more to the complexity.

## Goals

Provide an environment for tracking information about the events at the appropriate scale.

At the core, there is a concept of a **Document**, which represents documentation
of a specific activity in the infrastructure. For example, it can be an incident,
an observation about monitoring data, upgrade to a new version, utilization optimization.

The document consists of a set of blocks of different types, that construct all 
the important information for the document. It can be markdown note, link to 
alerts, arbitrary Kubernetes objects and anything else worth modeling for the
particular purpose. The system should be extensible to add more concepts as needed.

The different types of blocks allow choosing an appropriate level of structure for the information,
from an arbitrary format Markdown, to highly structured information that enabled integration
with other parts of the system.

Everything in the system is versioned and tracked, making it a safe space
for exploration and collaboration between human and machines.

## Example use-cases

### Incidents response

Let's consider an issue going on in a cluster or a service. It usually
demonstrates itself though a set of alerts that get triggered.

1. The alerts triage component watches the new alerts and applies appropriate
   heuristics to group them together. Each group is represented by a separate
   InfraPad incident document.

2. Each incident document will serve as place to keep all the information
   related to this event. When a troubleshooting agent is pointed to the
   incident, it can use it for loading the necessary context and write back the
   findings once finished.

3. The document doesn't capture only the information about the signal, but can
   track all the steps performed, that eventually led to the resolution of the
   issue.

InfraPad itself doesn't make any assumption about the way the incidents response
is implemented in the infrastructure, but it should be flexible enough to support
the needs in the particular environment.

The Markdown blocks allow to capture any arbitrary information while the structured
ones allow cross-linking to other systems and tools and make it easy to discover
the document later.

### Cluster upgrade

1. The administrator creates an InfraPad document to capture information about
   the upgrade.

2. As the automated pre-flight checks are performed, the results are captured in
   the upgrade document.

3. Eventually, once everything is ready for the upgrade, the `Upgrade Approved`
   block is added to the document.

4. The upgrade procedure keeps updating the document with progress about the
   upgrade. 

5. If any unexpected issue happens during the upgrade, any additional
   investigation can be also tracked in the document (including assigning
   alerts from the incidents response use-case to the upgrades document).

6. Eventually, as the upgrade finishes, the document provides the full timeline
   of the upgrade and can be used for auditing purposes.

### Resource optimization

Let's consider a resource optimization tool that would integrate with InfraPad.

1. The administrator uses the tool to analyze the utilization of the cluster.

2. Findings from the analysis are saved in a `Resource Optimization` document.

3. The optimization tool can provide custom visualization blocks that can help
understanding the findings and capture answers to users questions.

4. Once the changes are planned, the information about the changes can either
be captured in the doc itself, or a follow-up document can be created. 
