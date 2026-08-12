"""Generate browser-consumable person tracks for the demo alert clips.

The detector/tracker runs only during asset preparation. The deployed frontend
loads the generated JSON and does not ship Python, Ultralytics, or model weights.
"""

from __future__ import annotations

import json
from pathlib import Path

import cv2
from ultralytics import YOLO


WORKSPACE = Path(__file__).resolve().parents[1]
DEMO_DIR = WORKSPACE / "apps" / "web" / "public" / "demo"
OUTPUT_DIR = DEMO_DIR / "tracks"
MODEL_NAME = "yolo26n.pt"
VIDEOS = (
    "cctv-ch14-031430.mp4",
    "cctv-ch14-034100.mp4",
    "cctv-ch14-034329.mp4",
    "cctv-ch16-042352.mp4",
)


def normalized_box(xyxy: list[float], width: int, height: int) -> dict[str, float]:
    left, top, right, bottom = xyxy
    return {
        "x": round(max(0.0, min(1.0, left / width)), 6),
        "y": round(max(0.0, min(1.0, top / height)), 6),
        "width": round(max(0.0, min(1.0, (right - left) / width)), 6),
        "height": round(max(0.0, min(1.0, (bottom - top) / height)), 6),
    }


def track_video(video_path: Path) -> dict[str, object]:
    capture = cv2.VideoCapture(str(video_path))
    if not capture.isOpened():
        raise RuntimeError(f"Could not open {video_path}")

    fps = float(capture.get(cv2.CAP_PROP_FPS) or 15.0)
    width = int(capture.get(cv2.CAP_PROP_FRAME_WIDTH))
    height = int(capture.get(cv2.CAP_PROP_FRAME_HEIGHT))
    total_frames = int(capture.get(cv2.CAP_PROP_FRAME_COUNT))
    model = YOLO(MODEL_NAME)
    frames: list[dict[str, object]] = []
    frame_index = 0

    while True:
        success, frame = capture.read()
        if not success:
            break

        result = model.track(
            frame,
            persist=True,
            tracker="bytetrack.yaml",
            classes=[0],
            conf=0.18,
            iou=0.5,
            imgsz=640,
            device="cpu",
            verbose=False,
        )[0]
        detections: list[dict[str, object]] = []
        boxes = result.boxes
        if boxes is not None and len(boxes) > 0:
            coordinates = boxes.xyxy.cpu().tolist()
            confidences = boxes.conf.cpu().tolist()
            track_ids = boxes.id.int().cpu().tolist() if boxes.id is not None else list(range(1, len(boxes) + 1))
            for track_id, confidence, xyxy in zip(track_ids, confidences, coordinates, strict=True):
                detections.append(
                    {
                        "trackId": int(track_id),
                        "label": "Person",
                        "confidence": round(float(confidence), 4),
                        **normalized_box(xyxy, width, height),
                    }
                )

        frames.append({"time": round(frame_index / fps, 4), "boxes": detections})
        frame_index += 1

    capture.release()
    duration = frame_index / fps if fps else 0.0
    return {
        "version": 1,
        "source": video_path.name,
        "model": MODEL_NAME,
        "tracker": "ByteTrack",
        "class": "person",
        "fps": round(fps, 4),
        "width": width,
        "height": height,
        "duration": round(duration, 4),
        "sourceFrameCount": total_frames,
        "frames": frames,
    }


def main() -> None:
    OUTPUT_DIR.mkdir(parents=True, exist_ok=True)
    for name in VIDEOS:
        source = DEMO_DIR / name
        if not source.exists():
            raise FileNotFoundError(source)
        payload = track_video(source)
        target = OUTPUT_DIR / f"{source.stem}.json"
        target.write_text(json.dumps(payload, separators=(",", ":")), encoding="utf-8")
        tracked_frames = sum(bool(frame["boxes"]) for frame in payload["frames"])
        total_boxes = sum(len(frame["boxes"]) for frame in payload["frames"])
        print(f"{source.name}: {tracked_frames}/{len(payload['frames'])} frames, {total_boxes} person boxes -> {target.name}")


if __name__ == "__main__":
    main()
