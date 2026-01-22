# Problem Statement

This is a fledging repo for creating a Kubernetes Operator for Valkey and Valkey Cluster, created and maintained by the community. 

We would like to design the CRDs so that we have a vision to work towards.

## Requirements

- An RFC of the requirements was created here: https://github.com/valkey-io/valkey-rfc/pull/28
  - If that cannot be access, here is the raw link to the RFC: https://raw.githubusercontent.com/andrey-glazkov/valkey-rfc/04b6e3902092990aa821e2523d3fee99979304a8/VALKEY-K8S-OPERATOR.md
- There are also additional requirements from an infrastructure perspective found here: `/Users/joseph.heyburn/Obsidian/Get-Started/notes/Valkey Operator Requirements.md`
- There was a Github discussion with some findings here: `https://github.com/valkey-io/valkey-operator/discussions/19`
- A company that currently works at scale with Valkey on Kubernetes via helm charts (and are looking to adopt the Operator when it is ready) wrote about their design decisions for their internal helm-chart. They also include a section on an internal hackathon for a PoC operator based on their architecture: `https://gist.githubusercontent.com/jdheyburn/88c5c67625d784d52cb1245be68a7429/raw/2a82b71b0357461721db118aa12bcf8c3cb044ec/VALKEY_KUBERNETES_TOPOLOGIES.md`

## Additional design tips when designing Operators

See these two Obsidian notes, which were taken from KubeCon talks:

- `/Users/joseph.heyburn/Obsidian/Get-Started/notes/Kubernetes CRD Design for the Long Haul Tips, Tricks, and Lessons Learned.md`
- `/Users/joseph.heyburn/Obsidian/Get-Started/notes/Simplify Kubernetes Operator Development With a Modular Design Pattern.md`

## Additional information

We want to seek out the best practices and make this a best-in-class Operator. There are other successful operators for Prometheus, MongoDB, and ElasticSearch that exist today. We can seek inspiration for how they are set up from their respective code bases below:

- `/Users/joseph.heyburn/code/prometheus-operator`
- `/Users/joseph.heyburn/code/mongodb-kubernetes`
- `/Users/joseph.heyburn/code/elastic-cloud-on-k8s`

There are also unofficial Valkey Operators that exist. Research them to see if they are following any patterns or have ay features that we should include too.

- https://github.com/sap/valkey-operator
    - You can use context7 to read the docs for this
    - Code checked out at `/Users/joseph.heyburn/code/sap-valkey-operator`
- https://github.com/hyperspike/valkey-operator
    - Code checked out at `/Users/joseph.heyburn/code/hyperspike-valkey-operator`

You can use context7 to read the docs for:
  - Valkey
  - Kubernetes
  - sap/valkey-operator
