FROM python:3.11-slim

RUN apt-get update && apt-get install -y \
    fonts-dejavu-core \
    libcairo2 \
    && rm -rf /var/lib/apt/lists/*

# Створюємо робочу директорію
WORKDIR /app

# Копіюємо requirements
COPY requirements.txt .

# Встановлюємо Python залежності
RUN pip install --no-cache-dir -r requirements.txt

# Копіюємо код сервісу
COPY main.py draw_chart_svg.py ./

# Відкриваємо порт
EXPOSE 8000

# Запускаємо FastAPI через uvicorn
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
