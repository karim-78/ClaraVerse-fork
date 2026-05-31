FROM python:3.11-slim

# Minimal system deps — FastEmbed brings its own ONNX runtime via the
# fastembed wheel, no need for libonnxruntime separately.
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY main.py .

# Pre-warm strategy: we DO NOT bake the model into the image (~150 MB
# each, and we want admins to be able to swap without rebuilding). The
# first request to /embed triggers download into /root/.cache/fastembed.
# The volume mount in docker-compose persists this across restarts.

EXPOSE 8002

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8002", "--workers", "1"]
