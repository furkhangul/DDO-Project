import { describe, expect, it } from "vitest";

import { addDays, diffDays, toKurus } from "./format";

Object.assign(globalThis, { localStorage: { getItem: () => null, setItem: () => undefined, removeItem: () => undefined } });
const { idempotencyKey } = await import("./api");

describe("toKurus", () => {
  it("Türkçe ondalığı kuruşa çevirir", () => {
    expect(toKurus("1.250,75")).toBe(125075);
    expect(toKurus("19,99")).toBe(1999);
  });
  it("kayan nokta hatası üretmez", () => {
    expect(toKurus("1,15")).toBe(115);
  });
  it("geçersiz girdiyi işaretler", () => {
    expect(Number.isNaN(toKurus("abc"))).toBe(true);
  });
});

describe("tarih yardımcıları", () => {
  it("ay ve yıl sınırını aşar", () => {
    expect(addDays("2026-12-31", 1)).toBe("2027-01-01");
  });
  it("yaz saati geçişinde gün kaymaz", () => {
    expect(diffDays("2027-03-27", "2027-03-29")).toBe(2);
  });
});

describe("idempotencyKey", () => {
  it("güvenli bağlam gerektirmeden benzersiz anahtar üretir", () => {
    expect(idempotencyKey()).toMatch(/^[0-9a-f]{32}$/);
    expect(idempotencyKey()).not.toBe(idempotencyKey());
  });
});
