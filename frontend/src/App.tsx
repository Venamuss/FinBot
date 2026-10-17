import { useEffect, useState } from "react";
import { api, categories, errorMessage } from "./api";
import type {
  Category,
  Expense,
  ExpensePage,
  ExpenseQuery,
  Member,
  Order,
  Sort,
} from "./api";
import DeleteExpense from "./DeleteExpense";
import ExpenseEditor from "./ExpenseEditor";
import { displayAmount, displayDate, validDate } from "./validation";

const initialQuery: ExpenseQuery = {
  user_id: "",
  category: "",
  date_from: "",
  date_to: "",
  sort: "spent_at",
  order: "desc",
  limit: 20,
  offset: 0,
};

export default function App() {
  const [query, setQuery] = useState(initialQuery);
  const [page, setPage] = useState<ExpensePage>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [members, setMembers] = useState<Member[]>([]);
  const [memberError, setMemberError] = useState("");
  const [memberAttempt, setMemberAttempt] = useState(0);
  const [revision, setRevision] = useState(0);
  const [editor, setEditor] = useState<{ id: string | null }>();
  const [deleting, setDeleting] = useState<Expense>();
  const [notice, setNotice] = useState("");
  const dateError =
    (query.date_from && !validDate(query.date_from)) ||
    (query.date_to && !validDate(query.date_to))
      ? "Укажите существующие даты с годом от 0001 до 9999."
      : query.date_from && query.date_to && query.date_from > query.date_to
        ? "Конец периода не может быть раньше начала."
        : "";

  useEffect(() => {
    const controller = new AbortController();
    setMemberError("");
    api
      .members(controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) setMembers(result.items);
      })
      .catch((error) => {
        if (!controller.signal.aborted) setMemberError(errorMessage(error));
      });
    return () => controller.abort();
  }, [revision, memberAttempt]);

  useEffect(() => {
    const controller = new AbortController();
    setPage(undefined);
    setError("");
    setLoading(!dateError);
    if (!dateError) {
      api
        .list(query, controller.signal)
        .then((result) => {
          if (controller.signal.aborted) return;
          // Deleting the last row on a page or concurrent writes can move the last page.
          if (query.offset > 0 && query.offset >= result.total) {
            const offset =
              Math.max(0, Math.ceil(result.total / query.limit) - 1) *
              query.limit;
            setQuery((current) => ({ ...current, offset }));
            return;
          }
          setPage(result);
        })
        .catch((error) => {
          if (!controller.signal.aborted) setError(errorMessage(error));
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false);
        });
    }
    return () => controller.abort();
  }, [query, revision, dateError]);

  function changeQuery(patch: Partial<ExpenseQuery>) {
    setNotice("");
    setQuery((current) => ({ ...current, ...patch, offset: 0 }));
  }

  function saved(message: string) {
    setEditor(undefined);
    setDeleting(undefined);
    setNotice(message);
    setRevision((value) => value + 1);
  }

  const activeFilters = Boolean(
    query.user_id || query.category || query.date_from || query.date_to,
  );

  return (
    <>
      <a className="skip-link" href="#expenses">
        К списку расходов
      </a>
      <header className="masthead">
        <a className="brand" href="/" aria-label="FinBot, главная">
          FinBot<span aria-hidden="true">/</span>
        </a>
        <span>Семейный журнал</span>
      </header>
      <main>
        <div className="page-heading">
          <div>
            <p className="eyebrow">Общий учёт</p>
            <h1>Расходы семьи</h1>
            <p className="muted">Все участники, все повседневные траты.</p>
          </div>
          <button
            className="primary"
            onClick={() => {
              setNotice("");
              setEditor({ id: null });
            }}
          >
            + Добавить расход
          </button>
        </div>
        {notice && (
          <p className="notice success" role="status">
            {notice}
          </p>
        )}
        <section className="filters" aria-labelledby="filters-title">
          <div className="section-heading">
            <h2 id="filters-title">Фильтры</h2>
            <button
              className="quiet"
              onClick={() =>
                changeQuery({
                  user_id: "",
                  category: "",
                  date_from: "",
                  date_to: "",
                })
              }
              disabled={!activeFilters}
            >
              Сбросить фильтры
            </button>
          </div>
          <div className="filter-grid">
            <div className="field">
              <label htmlFor="filter-member">Участник</label>
              <select
                id="filter-member"
                value={query.user_id}
                onChange={(event) =>
                  changeQuery({ user_id: event.target.value })
                }
              >
                <option value="">Все участники</option>
                {query.user_id &&
                  !members.some(
                    (member) => member.user_id === query.user_id,
                  ) && <option value={query.user_id}>{query.user_id}</option>}
                {members.map((member) => (
                  <option key={member.user_id} value={member.user_id}>
                    {member.user_id}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label htmlFor="filter-category">Категория</label>
              <select
                id="filter-category"
                value={query.category}
                onChange={(event) =>
                  changeQuery({ category: event.target.value as Category | "" })
                }
              >
                <option value="">Все категории</option>
                {categories.map((category) => (
                  <option key={category} value={category}>
                    {category}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label htmlFor="date-from">Дата с</label>
              <input
                id="date-from"
                type="date"
                min="0001-01-01"
                max="9999-12-31"
                value={query.date_from}
                aria-invalid={Boolean(dateError)}
                aria-describedby={dateError ? "date-error" : undefined}
                onChange={(event) =>
                  changeQuery({ date_from: event.target.value })
                }
              />
            </div>
            <div className="field">
              <label htmlFor="date-to">Дата по</label>
              <input
                id="date-to"
                type="date"
                min="0001-01-01"
                max="9999-12-31"
                value={query.date_to}
                aria-invalid={Boolean(dateError)}
                aria-describedby={dateError ? "date-error" : undefined}
                onChange={(event) =>
                  changeQuery({ date_to: event.target.value })
                }
              />
            </div>
          </div>
          {dateError && (
            <p id="date-error" role="alert" className="field-error">
              {dateError}
            </p>
          )}
          {memberError && (
            <div className="notice error" role="alert">
              <p>Не удалось загрузить участников. {memberError}</p>
              <button onClick={() => setMemberAttempt((value) => value + 1)}>
                Повторить загрузку участников
              </button>
            </div>
          )}
        </section>
        <section
          id="expenses"
          className="expenses"
          aria-labelledby="expenses-title"
          aria-busy={loading}
        >
          <div className="list-toolbar">
            <h2 id="expenses-title">
              Журнал расходов
              {page && <span className="count">{page.total}</span>}
            </h2>
            <div className="sort-controls">
              <label htmlFor="sort">Сортировать</label>
              <select
                id="sort"
                value={query.sort}
                onChange={(event) =>
                  changeQuery({ sort: event.target.value as Sort })
                }
              >
                <option value="spent_at">По дате расхода</option>
                <option value="created_at">По дате добавления</option>
                <option value="amount">По сумме</option>
                <option value="id">По ID записи</option>
              </select>
              <label className="sr-only" htmlFor="order">
                Порядок сортировки
              </label>
              <select
                id="order"
                value={query.order}
                onChange={(event) =>
                  changeQuery({ order: event.target.value as Order })
                }
              >
                <option value="desc">По убыванию</option>
                <option value="asc">По возрастанию</option>
              </select>
            </div>
          </div>
          {loading && (
            <div className="state" role="status">
              <p>Загружаем расходы…</p>
            </div>
          )}
          {error && (
            <div className="state" role="alert">
              <h3>Не удалось загрузить расходы</h3>
              <p>{error}</p>
              <button onClick={() => setRevision((value) => value + 1)}>
                Повторить
              </button>
            </div>
          )}
          {!loading && !error && page?.items.length === 0 && (
            <div className="state">
              <h3>
                {activeFilters
                  ? "Расходов по этим фильтрам нет"
                  : "Пока нет расходов"}
              </h3>
              <p className="muted">
                {activeFilters
                  ? "Попробуйте изменить период, участника или категорию."
                  : "Добавьте первый расход кнопкой выше или через Telegram."}
              </p>
            </div>
          )}
          {!loading && !error && page && page.items.length > 0 && (
            <div
              className="table-scroll"
              tabIndex={0}
              role="region"
              aria-label="Таблица расходов"
            >
              <table>
                <caption className="sr-only">
                  Расходы всех участников с учётом выбранных фильтров
                </caption>
                <thead>
                  <tr>
                    <th scope="col">Участник</th>
                    <th scope="col" className="numeric">
                      Сумма
                    </th>
                    <th scope="col">Категория</th>
                    <th scope="col">Дата расхода</th>
                    <th scope="col">Комментарий</th>
                    <th scope="col">
                      <span className="sr-only">Действия</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {page.items.map((expense) => (
                    <tr key={expense.id}>
                      <td className="member-id">{expense.user_id}</td>
                      <td className="numeric amount">
                        {displayAmount(expense.amount)}
                      </td>
                      <td>
                        <span className="category">{expense.category}</span>
                      </td>
                      <td className="date">
                        <time dateTime={expense.spent_at}>
                          {displayDate(expense.spent_at)}
                        </time>
                      </td>
                      <td className="comment">
                        {expense.comment === null || expense.comment === "" ? (
                          <span className="muted">—</span>
                        ) : (
                          expense.comment
                        )}
                      </td>
                      <td>
                        <div className="row-actions">
                          <button
                            className="quiet"
                            aria-label={`Редактировать расход ${expense.id}`}
                            onClick={() => {
                              setNotice("");
                              setEditor({ id: expense.id });
                            }}
                          >
                            Изменить
                          </button>
                          <button
                            className="quiet"
                            aria-label={`Удалить расход ${expense.id}`}
                            onClick={() => {
                              setNotice("");
                              setDeleting(expense);
                            }}
                          >
                            Удалить
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          <div className="pagination">
            <div className="page-size">
              <label htmlFor="page-size">Записей на странице</label>
              <select
                id="page-size"
                value={query.limit}
                onChange={(event) =>
                  changeQuery({ limit: Number(event.target.value) })
                }
              >
                <option value="20">20</option>
                <option value="50">50</option>
                <option value="100">100</option>
              </select>
            </div>
            {page && !loading && !error && (
              <span className="muted" aria-live="polite">
                {page.total === 0
                  ? "0 записей"
                  : `${page.offset + 1}–${Math.min(page.offset + page.items.length, page.total)} из ${page.total}`}
              </span>
            )}
            <div className="page-actions">
              <button
                disabled={
                  loading || !page || Boolean(error) || query.offset === 0
                }
                onClick={() =>
                  setQuery((current) => ({
                    ...current,
                    offset: Math.max(0, current.offset - current.limit),
                  }))
                }
              >
                Назад
              </button>
              <button
                disabled={
                  loading ||
                  !page ||
                  Boolean(error) ||
                  query.offset + query.limit >= page.total
                }
                onClick={() =>
                  setQuery((current) => ({
                    ...current,
                    offset: current.offset + current.limit,
                  }))
                }
              >
                Далее
              </button>
            </div>
          </div>
        </section>
        <footer className="page-footer">
          Участники указаны по Telegram ID. Доступ к панели не требует входа.
        </footer>
      </main>
      {editor && (
        <ExpenseEditor
          id={editor.id}
          members={members}
          onClose={() => setEditor(undefined)}
          onSaved={saved}
        />
      )}
      {deleting && (
        <DeleteExpense
          expense={deleting}
          onClose={() => setDeleting(undefined)}
          onDeleted={() => saved("Расход удалён.")}
        />
      )}
    </>
  );
}
