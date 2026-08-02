# Third-Party Notices

Status: incomplete review artifact; not approved for publication
Reviewed: 2026-07-26

This repository builds software that incorporates or redistributes third-party
components. The immutable release bundle contains one SPDX 2.3 SBOM per image.
Those SBOMs, the image filesystem license files, and upstream source archives
are the package-level inventory.

Major redistributed components include:

- Xray-core 26.3.27, Mozilla Public License 2.0;
- Prometheus 3.13.1, Apache License 2.0;
- Grafana 13.1.1, GNU Affero General Public License 3.0;
- OpenTelemetry Collector 0.157.0, Apache License 2.0;
- Tempo 2.10.5, GNU Affero General Public License 3.0;
- Loki 3.7.2, GNU Affero General Public License 3.0;
- PostgreSQL 18 client/backup tooling, PostgreSQL License;
- age 1.3.1, BSD 3-Clause License;
- Go and npm dependencies identified by the release SBOMs and lockfiles.

Prometheus, Collector, Tempo, Loki, and age license material is copied by the
relevant Dockerfile. Grafana uses its upstream image and source rebuild. Xray is
built from pinned source.

This file is not yet a complete notice or corresponding-source offer. Release
publication is prohibited until `LIC-01` through `LIC-06` in
`docs/reviews/stage9/dependency-license-review.md` are closed by the named
owners and professional counsel where required.
