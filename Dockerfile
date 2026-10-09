FROM python:3.12-slim
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1 LANBRIDGE_USE_VENV=1
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends iproute2 iputils-ping traceroute dnsutils \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 lanbridge && useradd --uid 10001 --gid 10001 --no-create-home lanbridge
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY backend ./backend
COPY frontend ./frontend
COPY scripts ./scripts
COPY run.py VERSION config.example.yaml ./
RUN mkdir -p /app/data && chown -R 10001:10001 /app/data
USER 10001:10001
EXPOSE 8765
CMD ["python", "run.py"]
