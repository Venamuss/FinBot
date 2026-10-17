import { describe, expect, it } from "vitest";
import {
  changedFields,
  formInput,
  validateForm,
  validDate,
} from "./validation";
import type { FormValues } from "./validation";
import type { Expense } from "./api";

const valid: FormValues = {
  user_id: "9223372036854775807",
  amount: "0.01",
  category: "еда",
  spent_at: "0001-01-01",
  comment: "",
};

describe("API contract validation", () => {
  it.each([
    "0",
    "0.00",
    "1.001",
    "1000000000000",
    "1e3",
    "1,50",
    "-1",
    " 1",
    "01",
  ])("rejects amount %s without rounding", (amount) => {
    expect(validateForm({ ...valid, amount }).amount).toBeTruthy();
  });
  it.each(["350", "350.5", "350.50", "0.01", "999999999999.99"])(
    "accepts exact decimal %s",
    (amount) => {
      expect(validateForm({ ...valid, amount })).toEqual({});
    },
  );
  it.each(["0", "01", "9223372036854775808", "1e3", "-1", ""])(
    "rejects invalid owner %s",
    (user_id) => {
      expect(validateForm({ ...valid, user_id }).user_id).toBeTruthy();
    },
  );
  it("checks calendar dates, categories and nul comments", () => {
    expect(validDate("2024-02-29")).toBe(true);
    expect(validDate("2026-02-29")).toBe(false);
    expect(validDate("0000-01-01")).toBe(false);
    expect(validDate("10000-01-01")).toBe(false);
    expect(
      validateForm({ ...valid, category: "Еда", comment: "\0" }),
    ).toHaveProperty("category");
    expect(validateForm({ ...valid, comment: "\0" })).toHaveProperty("comment");
  });
  it.each([null, ""])("preserves untouched comment %s in PATCH", (comment) => {
    const original: Expense = {
      ...valid,
      amount: "0.01",
      category: "еда",
      id: "1",
      comment,
      created_at: "",
      updated_at: "",
    };
    expect(changedFields(formInput(valid, original), original)).toEqual({});
  });
});
