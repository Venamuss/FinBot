import type { Expense, ExpenseInput } from "./api";
import { categories } from "./api";

export interface FormValues {
  user_id: string;
  amount: string;
  category: string;
  spent_at: string;
  comment: string;
}

export function validDate(value: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value) || value.startsWith("0000"))
    return false;
  const date = new Date(`${value}T00:00:00Z`);
  return (
    !Number.isNaN(date.getTime()) && date.toISOString().slice(0, 10) === value
  );
}

export function validateForm(values: FormValues): Record<string, string> {
  const errors: Record<string, string> = {};
  if (
    !/^[1-9]\d{0,18}$/.test(values.user_id) ||
    BigInt(values.user_id) > 9223372036854775807n
  ) {
    errors.user_id =
      "Укажите Telegram ID от 1 до 9223372036854775807 без ведущих нулей.";
  }
  if (
    !/^(0|[1-9]\d{0,11})(\.\d{1,2})?$/.test(values.amount) ||
    !/[1-9]/.test(values.amount)
  ) {
    errors.amount =
      "Введите сумму от 0.01 до 999999999999.99 с точкой и не более двух знаков после неё.";
  }
  if (!categories.some((category) => category === values.category))
    errors.category = "Выберите категорию.";
  if (!validDate(values.spent_at))
    errors.spent_at = "Укажите существующую дату с годом от 0001 до 9999.";
  if (values.comment.includes("\0"))
    errors.comment = "Нулевой символ не поддерживается.";
  return errors;
}

export function normalizeAmount(value: string): string {
  const [whole, fraction = ""] = value.split(".");
  return `${whole}.${fraction.padEnd(2, "0")}`;
}

export function formInput(
  values: FormValues,
  original?: Expense,
): ExpenseInput {
  return {
    user_id: values.user_id,
    amount: normalizeAmount(values.amount),
    category: values.category as ExpenseInput["category"],
    spent_at: values.spent_at,
    // Preserve an existing empty string; clearing a nonempty comment sends null.
    comment:
      original && values.comment === (original.comment ?? "")
        ? original.comment
        : values.comment === ""
          ? null
          : values.comment,
  };
}

export function changedFields(
  input: ExpenseInput,
  original: Expense,
): Partial<ExpenseInput> {
  const patch: Partial<ExpenseInput> = {};
  if (input.user_id !== original.user_id) patch.user_id = input.user_id;
  if (input.amount !== original.amount) patch.amount = input.amount;
  if (input.category !== original.category) patch.category = input.category;
  if (input.spent_at !== original.spent_at) patch.spent_at = input.spent_at;
  if (input.comment !== original.comment) patch.comment = input.comment;
  return patch;
}

export function today(): string {
  const now = new Date();
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
}

export function displayDate(value: string): string {
  return value.split("-").reverse().join(".");
}

export function displayAmount(value: string): string {
  const [whole, fraction] = value.split(".");
  return `${whole.replace(/\B(?=(\d{3})+(?!\d))/g, "\u00a0")},${fraction}`;
}
