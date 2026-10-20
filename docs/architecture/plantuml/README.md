# PlantUML diagrams

Source `.puml` files; rendered output in `png/` and `svg/`.

| # | Diagram | Type |
|---|---|---|
| 01 | [System context](png/01-system-context.png) | Use-case / context |
| 02 | [Containers](png/02-containers.png) | Component |
| 03 | [Go package layers](png/03-package-layers.png) | Component |
| 04 | [Core domain model](png/04-domain-model.png) | Class |
| 05 | [Search / recall / feedback](png/05-recall-sequence.png) | Sequence |
| 06 | [Harness run state machine](png/06-harness-run-state.png) | State |
| 07 | [Harness tool call and approval](png/07-harness-tool-approval.png) | Sequence |
| 08 | [Roadmap status](png/08-roadmap-status.png) | WBS (from `tasks.md` checkboxes) |
| 09 | [Proposed roadmap](png/09-roadmap-proposed.png) | Gantt (proposal, not committed dates) |

Re-render after editing:

```bash
java -Djava.awt.headless=true -jar plantuml.jar -tpng -o png *.puml
java -Djava.awt.headless=true -jar plantuml.jar -tsvg -o svg *.puml
```
