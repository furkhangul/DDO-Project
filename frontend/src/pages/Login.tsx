import { useState, type FormEvent } from "react";
import { Field } from "../components/ui";
import { login } from "../lib/api";
import { roleLabels } from "../lib/format";

const demo = [
  ["reception", "resepsiyon@konakpro.test"],
  ["reservations", "rezervasyon@konakpro.test"],
  ["housekeeping", "kathizmeti@konakpro.test"],
  ["revenue", "gelir@konakpro.test"],
  ["manager", "mudur@konakpro.test"],
  ["guest", "misafir@konakpro.test"],
];

export function Login() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      await login(email, password);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : "Giriş yapılamadı");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login">
      <div className="login-art">
        <img src="/konak.svg" alt="" width={44} height={44} />
        <p className="login-title">Konak Palas Otel</p>
        <p>120 oda, tek ekran. Rezervasyondan gece denetimine otelin bütün günü burada.</p>
      </div>
      <form className="login-form" onSubmit={submit}>
        <h1>Giriş yap</h1>
        {error && (
          <p className="form-error" role="alert">
            {error}
          </p>
        )}
        <Field label="E-posta">
          {(id) => <input id={id} type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />}
        </Field>
        <Field label="Parola">
          {(id) => <input id={id} type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />}
        </Field>
        <button className="btn primary wide" disabled={busy}>
          {busy ? "Giriş yapılıyor…" : "Giriş yap"}
        </button>
        <details className="demo">
          <summary>Demo hesapları</summary>
          <div>
            {demo.map(([role, address]) => (
              <button
                type="button"
                key={role}
                className="chip"
                onClick={() => {
                  setEmail(address);
                  setPassword("Konak!2026");
                }}
              >
                {roleLabels[role]}
              </button>
            ))}
          </div>
        </details>
      </form>
    </div>
  );
}
