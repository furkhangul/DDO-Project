import { useEffect, useRef, useState } from "react";
import { accessToken, refreshSession } from "./api";

export interface LiveEvent {
  id: number;
  actor: string;
  action: string;
  entity: string;
  entity_id: string;
  at: string;
}

export function useLiveFeed(enabled: boolean, limit = 30) {
  const [events, setEvents] = useState<LiveEvent[]>([]);
  const [connected, setConnected] = useState(false);
  const stop = useRef<AbortController | null>(null);

  useEffect(() => {
    if (!enabled) return;
    let closed = false;
    const consume = async () => {
      while (!closed) {
        const controller = new AbortController();
        stop.current = controller;
        try {
          const response = await fetch("/api/v1/live", {
            headers: { Authorization: `Bearer ${accessToken()}` },
            signal: controller.signal,
          });
          if (response.status === 401) {
            await refreshSession();
            continue;
          }
          if (!response.ok || !response.body) throw new Error("akış açılamadı");
          setConnected(true);
          const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
          let buffer = "";
          for (;;) {
            const { value, done } = await reader.read();
            if (done) break;
            buffer += value;
            const frames = buffer.split("\n\n");
            buffer = frames.pop() ?? "";
            for (const frame of frames) {
              if (!frame.startsWith("data: ")) continue;
              const event = JSON.parse(frame.slice(6)) as LiveEvent;
              setEvents((current) => [event, ...current].slice(0, limit));
            }
          }
        } catch {
          if (closed) return;
        }
        setConnected(false);
        await new Promise((resolve) => setTimeout(resolve, 2500));
      }
    };
    void consume();
    return () => {
      closed = true;
      stop.current?.abort();
    };
  }, [enabled, limit]);

  return { events, connected };
}

const labels: Record<string, string> = {
  "reservation.created": "yeni rezervasyon",
  "reservation.modified": "rezervasyon değişikliği",
  "reservation.cancelled": "iptal",
  "reservation.checked_in": "giriş yaptı",
  "reservation.checked_out": "çıkış yaptı",
  "reservation.walked": "misafir yönlendirildi",
  "reservation.no_show": "gelmedi",
  "channel.event_applied": "kanal olayı işlendi",
  "channel.event_stale": "eski sıralı olay atlandı",
  "channel.event_conflict": "kanal çakışması",
  "channel.event_rejected": "kanal olayı reddedildi",
  "folio.payment": "ödeme alındı",
  "folio.charge": "harcama eklendi",
  "folio.refund": "iade yapıldı",
  "folio.split": "folyo bölündü",
  "assignment.applied": "oda planı uygulandı",
  "assignment.moved": "oda değişikliği",
  "night_audit.completed": "gece denetimi tamamlandı",
  "room.status_changed": "oda durumu güncellendi",
};

export const describeAction = (action: string) => labels[action] ?? action;
