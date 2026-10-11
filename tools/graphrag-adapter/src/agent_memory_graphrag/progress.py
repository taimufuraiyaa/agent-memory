from __future__ import annotations

import asyncio
from dataclasses import asdict, dataclass


class CancellationToken:
    def __init__(self) -> None:
        self._cancelled = False

    def cancel(self) -> None:
        self._cancelled = True

    @property
    def cancelled(self) -> bool:
        return self._cancelled

    def checkpoint(self) -> None:
        if self._cancelled:
            raise asyncio.CancelledError


@dataclass(frozen=True)
class ProgressEvent:
    sequence: int
    kind: str
    workflow: str = ""
    detail: str = ""

    def to_dict(self) -> dict[str, object]:
        return asdict(self)


class ProgressRecorder:
    def __init__(self, cancellation: CancellationToken) -> None:
        self.cancellation = cancellation
        self.events: list[ProgressEvent] = []

    def _append(self, kind: str, workflow: str = "", detail: str = "") -> None:
        self.cancellation.checkpoint()
        self.events.append(ProgressEvent(len(self.events) + 1, kind, workflow, detail))

    def pipeline_start(self, names: list[str]) -> None:
        self._append("pipeline_start", detail=",".join(names))

    def pipeline_end(self, results: list[object]) -> None:
        self._append("pipeline_end", detail=str(len(results)))

    def workflow_start(self, name: str, instance: object) -> None:
        del instance
        self._append("workflow_start", name)

    def workflow_end(self, name: str, instance: object) -> None:
        del instance
        self._append("workflow_end", name)

    def progress(self, progress: object) -> None:
        self._append("progress", detail=str(progress)[:512])

    def pipeline_error(self, error: BaseException) -> None:
        self._append("pipeline_error", detail=type(error).__name__)
