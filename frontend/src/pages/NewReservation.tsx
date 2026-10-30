import { useMutation, useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { QuoteTable, type Quote } from "../components/QuoteTable";
import { Card, ErrorState, Field, Loading, Page, useToast } from "../components/ui";
import { api, ApiError } from "../lib/api";
import { addDays, tl } from "../lib/format";

interface RoomType {
  id: number;
  code: string;
  name: string;
  max_occupancy: number;
}

interface Plan {
  id: number;
  code: string;
  name: string;
}

export function useBookingOptions() {
  const types = useQuery({ queryKey: ["room-types"], queryFn: () => api<{ items: RoomType[] }>("/room-types") });
  const plans = useQuery({ queryKey: ["rate-plans"], queryFn: () => api<{ items: Plan[] }>("/rate-plans") });
  return { types, plans };
}

export interface StayForm {
  room_type_id: number;
  rate_plan_id: number;
  check_in: string;
  check_out: string;
  adults: number;
  children: string;
  codes: string;
}

export const stayPayload = (form: StayForm) => ({
  room_type_id: form.room_type_id,
  rate_plan_id: form.rate_plan_id,
  check_in: form.check_in,
  check_out: form.check_out,
  adults: form.adults,
  children_ages: form.children.split(",").map((age) => age.trim()).filter(Boolean).map(Number),
  codes: form.codes.split(",").map((code) => code.trim()).filter(Boolean),
});

export function StayFields({ form, set, types, plans }: { form: StayForm; set: (patch: Partial<StayForm>) => void; types: RoomType[]; plans: Plan[] }) {
  return (
    <div className="form-grid">
      <Field label="Oda tipi">
        {(id) => (
          <select id={id} value={form.room_type_id} onChange={(e) => set({ room_type_id: Number(e.target.value) })}>
            {types.map((type) => (
              <option key={type.id} value={type.id}>
                {type.name}
              </option>
            ))}
          </select>
        )}
      </Field>
      <Field label="Tarife">
        {(id) => (
          <select id={id} value={form.rate_plan_id} onChange={(e) => set({ rate_plan_id: Number(e.target.value) })}>
            {plans.map((plan) => (
              <option key={plan.id} value={plan.id}>
                {plan.name}
              </option>
            ))}
          </select>
        )}
      </Field>
      <Field label="Giriş">{(id) => <input id={id} type="date" required value={form.check_in} onChange={(e) => set({ check_in: e.target.value })} />}</Field>
      <Field label="Çıkış">{(id) => <input id={id} type="date" required value={form.check_out} onChange={(e) => set({ check_out: e.target.value })} />}</Field>
      <Field label="Yetişkin">{(id) => <input id={id} type="number" min={1} max={10} value={form.adults} onChange={(e) => set({ adults: Number(e.target.value) })} />}</Field>
      <Field label="Çocuk yaşları" hint="Virgülle: 5, 9">
        {(id) => <input id={id} inputMode="numeric" value={form.children} onChange={(e) => set({ children: e.target.value })} />}
      </Field>
      <Field label="Kampanya kodları" hint="Virgülle: HOSGELDIN10, AILE5">
        {(id) => <input id={id} value={form.codes} onChange={(e) => set({ codes: e.target.value.toUpperCase() })} />}
      </Field>
    </div>
  );
}

export default function NewReservation() {
  const { types, plans } = useBookingOptions();
  const today = useQuery({ queryKey: ["dashboard"], queryFn: () => api<{ business_date: string }>("/dashboard") });
  if (types.isPending || plans.isPending || today.isPending) return <Loading />;
  if (!types.data || !plans.data || !today.data) return <ErrorState error={types.error ?? plans.error ?? today.error} />;
  return <ReservationForm types={types.data.items} plans={plans.data.items} businessDate={today.data.business_date} />;
}

function ReservationForm({ types, plans, businessDate }: { types: RoomType[]; plans: Plan[]; businessDate: string }) {
  const navigate = useNavigate();
  const toast = useToast();
  const [touched, setTouched] = useState<Partial<StayForm>>({});
  const [guest, setGuest] = useState({ full_name: "", email: "", phone: "" });
  const [prefs, setPrefs] = useState({ floor: "", view: "", group: "" });
  const [quote, setQuote] = useState<Quote | null>(null);

  const form: StayForm = {
    room_type_id: types[0].id,
    rate_plan_id: plans[0].id,
    check_in: addDays(businessDate, 7),
    check_out: addDays(businessDate, 9),
    adults: 2,
    children: "",
    codes: "",
    ...touched,
  };

  const price = useQuoteAction(form, setQuote);
  const create = useMutation({
    mutationFn: () =>
      api<{ id: number; code: string }>("/reservations", {
        method: "POST",
        idempotent: true,
        body: {
          ...stayPayload(form),
          guest: { full_name: guest.full_name, email: guest.email || null, phone: guest.phone || null },
          pref_floor: prefs.floor ? Number(prefs.floor) : null,
          pref_view: prefs.view || null,
          adjacent_group: prefs.group || null,
          quote_hash: quote?.hash,
        },
      }),
    onSuccess: (result) => {
      toast(`${result.code} oluşturuldu`);
      navigate(`/rezervasyonlar/${result.id}`);
    },
    onError: (error) => {
      if (error instanceof ApiError && error.code === "PRICE_CHANGED") setQuote(null);
      toast(error instanceof Error ? error.message : "Rezervasyon oluşturulamadı", true);
    },
  });

  const change = (patch: Partial<StayForm>) => {
    setTouched({ ...touched, ...patch });
    setQuote(null);
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (quote) create.mutate();
    else price.mutate();
  };

  return (
    <Page title="Yeni rezervasyon" note="Önce fiyatı hesaplayın. Onaylanan fiyat rezervasyona kilitlenir, sonraki kural değişikliklerinden etkilenmez.">
      <form onSubmit={submit} className="stack">
        <Card title="Konaklama">
          <StayFields form={form} set={change} types={types} plans={plans} />
        </Card>
        <Card title="Misafir">
          <div className="form-grid">
            <Field label="Ad soyad">{(id) => <input id={id} required value={guest.full_name} onChange={(e) => setGuest({ ...guest, full_name: e.target.value })} />}</Field>
            <Field label="E-posta">{(id) => <input id={id} type="email" value={guest.email} onChange={(e) => setGuest({ ...guest, email: e.target.value })} />}</Field>
            <Field label="Telefon">{(id) => <input id={id} type="tel" value={guest.phone} onChange={(e) => setGuest({ ...guest, phone: e.target.value })} />}</Field>
          </div>
        </Card>
        <Card title="Özel istekler">
          <div className="form-grid">
            <Field label="Kat">{(id) => <input id={id} type="number" min={1} max={7} value={prefs.floor} onChange={(e) => setPrefs({ ...prefs, floor: e.target.value })} />}</Field>
            <Field label="Manzara">
              {(id) => (
                <select id={id} value={prefs.view} onChange={(e) => setPrefs({ ...prefs, view: e.target.value })}>
                  <option value="">Fark etmez</option>
                  <option value="deniz">Deniz</option>
                  <option value="bahce">Bahçe</option>
                  <option value="sehir">Şehir</option>
                </select>
              )}
            </Field>
            <Field label="Bitişik oda grubu" hint="Aynı grup adlı rezervasyonlar yan yana yerleştirilir">
              {(id) => <input id={id} value={prefs.group} onChange={(e) => setPrefs({ ...prefs, group: e.target.value })} />}
            </Field>
          </div>
        </Card>
        {quote && (
          <Card title="Gün gün fiyat" aside={<strong>{tl(quote.total_kurus)}</strong>}>
            <QuoteTable quote={quote} />
          </Card>
        )}
        <div className="form-actions">
          <button type="button" className="btn" onClick={() => navigate(-1)}>
            Vazgeç
          </button>
          <button className="btn primary" disabled={price.isPending || create.isPending}>
            {quote ? "Rezervasyonu onayla" : price.isPending ? "Hesaplanıyor…" : "Fiyatı hesapla"}
          </button>
        </div>
      </form>
    </Page>
  );
}

export function useQuoteAction(form: StayForm, onQuote: (quote: Quote) => void) {
  const toast = useToast();
  return useMutation({
    mutationFn: () => api<Quote>("/quotes", { method: "POST", body: stayPayload(form) }),
    onSuccess: onQuote,
    onError: (error) => toast(error instanceof Error ? error.message : "Fiyat hesaplanamadı", true),
  });
}
