import { createContext, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { statusLabels } from "../lib/format";

export function Page({ title, note, actions, children }: { title: string; note?: ReactNode; actions?: ReactNode; children: ReactNode }) {
  return (
    <section className="page">
      <header className="page-head">
        <div>
          <h1>{title}</h1>
          {note && <p className="muted">{note}</p>}
        </div>
        {actions && <div className="page-actions">{actions}</div>}
      </header>
      {children}
    </section>
  );
}

export function Card({ title, aside, children, flush }: { title?: ReactNode; aside?: ReactNode; children: ReactNode; flush?: boolean }) {
  return (
    <div className={flush ? "card flush" : "card"}>
      {(title || aside) && (
        <div className="card-head">
          <h2>{title}</h2>
          {aside}
        </div>
      )}
      {children}
    </div>
  );
}

export function Stat({ label, value, hint, tone }: { label: string; value: ReactNode; hint?: ReactNode; tone?: "warn" | "bad" }) {
  return (
    <div className={`stat ${tone ?? ""}`}>
      <span className="stat-label">{label}</span>
      <strong>{value}</strong>
      {hint && <span className="muted small">{hint}</span>}
    </div>
  );
}

const tones: Record<string, string> = {
  confirmed: "info",
  checked_in: "good",
  checked_out: "plain",
  cancelled: "plain",
  no_show: "warn",
  walked: "warn",
  dirty: "warn",
  clean: "good",
  inspected: "good",
  out_of_order: "bad",
  bekliyor: "warn",
  devam: "info",
  tamamlandi: "good",
  applied: "good",
  stale: "plain",
  conflict: "bad",
  rejected: "bad",
  pending: "info",
  sending: "info",
  sent: "good",
  dead: "bad",
  superseded: "plain",
  running: "warn",
  completed: "good",
};

export function Badge({ status }: { status: string }) {
  return <span className={`badge ${tones[status] ?? "plain"}`}>{statusLabels[status] ?? status}</span>;
}

export function Loading({ label = "Yükleniyor" }: { label?: string }) {
  return (
    <div className="state" role="status">
      <span className="spinner" aria-hidden />
      {label}
    </div>
  );
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="state empty">
      <strong>{title}</strong>
      {children && <span className="muted">{children}</span>}
    </div>
  );
}

export function ErrorState({ error, retry }: { error: unknown; retry?: () => void }) {
  return (
    <div className="state error" role="alert">
      <strong>Bu veri yüklenemedi</strong>
      <span>{error instanceof Error ? error.message : "Beklenmeyen bir hata oluştu"}</span>
      {retry && (
        <button className="btn" onClick={retry}>
          Yeniden dene
        </button>
      )}
    </div>
  );
}

interface QueryLike<T> {
  data: T | undefined;
  isPending: boolean;
  error: unknown;
  refetch: () => unknown;
}

export function Query<T>({ query, children, empty }: { query: QueryLike<T>; children: (data: T) => ReactNode; empty?: (data: T) => ReactNode }) {
  if (query.isPending) return <Loading />;
  if (query.error || query.data === undefined) return <ErrorState error={query.error} retry={() => query.refetch()} />;
  return <>{empty?.(query.data) ?? children(query.data)}</>;
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: (id: string) => ReactNode }) {
  const id = useId();
  return (
    <div className="field">
      <label htmlFor={id}>{label}</label>
      {children(id)}
      {hint && <span className="muted small">{hint}</span>}
    </div>
  );
}

export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    dialog?.showModal();
    return () => dialog?.close();
  }, []);
  return (
    <dialog ref={ref} className="modal" onCancel={onClose} aria-label={title}>
      <header>
        <h2>{title}</h2>
        <button className="btn ghost" onClick={onClose} aria-label="Kapat">
          ✕
        </button>
      </header>
      {children}
    </dialog>
  );
}

type Toast = { id: number; text: string; bad: boolean };
const ToastContext = createContext<(text: string, bad?: boolean) => void>(() => undefined);

export function ToastHost({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const counter = useRef(0);
  const push = useCallback((text: string, bad = false) => {
    const id = ++counter.current;
    setToasts((items) => [...items, { id, text, bad }]);
    setTimeout(() => setToasts((items) => items.filter((item) => item.id !== id)), 5000);
  }, []);
  const value = useMemo(() => push, [push]);
  return (
    <ToastContext.Provider value={value}>
      {children}
      <div className="toasts" aria-live="polite">
        {toasts.map((toast) => (
          <div key={toast.id} className={toast.bad ? "toast bad" : "toast"}>
            {toast.text}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export const useToast = () => useContext(ToastContext);
