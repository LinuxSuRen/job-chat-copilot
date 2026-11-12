/**
 * 搜索页职位卡片扫描（jobs.scan → jobs.list，§5.1 契约）。
 * 结构化/匹配逻辑为纯函数（单测覆盖），DOM 解析薄封装在此收敛。
 */

import { extractJobKey } from '../selectors';
import type { JobsListPayload, ScannedJob } from '../shared/protocol';

const JD_TEXT_LIMIT = 500;

/** 纯：单条扫描条目构造。job_key 提取失败视为无效卡片（返回 null）。 */
export function toScannedJob(
  href: string,
  title: string,
  company: string,
  salary: string,
  jdText: string,
): ScannedJob | null {
  const job_key = extractJobKey(href);
  if (!job_key) {
    return null;
  }
  const jd_text = jdText.trim().slice(0, JD_TEXT_LIMIT);
  return {
    job_key,
    title: title.trim(),
    company: company.trim(),
    salary: salary.trim(),
    jd_text,
  };
}

/** 纯：jobs.list 载荷构造（去重：同 job_key 保留首个） */
export function buildJobsList(keyword: string, jobs: ScannedJob[]): JobsListPayload {
  const seen = new Set<string>();
  const deduped: ScannedJob[] = [];
  for (const j of jobs) {
    if (seen.has(j.job_key)) {
      continue;
    }
    seen.add(j.job_key);
    deduped.push(j);
  }
  return { keyword, jobs: deduped };
}

export interface JobTarget {
  job_key?: string;
  job_title?: string;
  company?: string;
}

/**
 * 纯：deliver.next 的卡片定位——job_key 优先精确匹配，job_title 兜底（标题互相包含
 * 且公司名一致/未提供时命中）。返回首个命中的条目。
 */
export function matchJob(jobs: ScannedJob[], target: JobTarget): ScannedJob | null {
  if (target.job_key) {
    const byKey = jobs.find((j) => j.job_key === target.job_key);
    if (byKey) {
      return byKey;
    }
  }
  if (!target.job_title) {
    return null;
  }
  return (
    jobs.find((j) => {
      const titleHit = j.title.includes(target.job_title!) || target.job_title!.includes(j.title);
      if (!titleHit) {
        return false;
      }
      if (!target.company) {
        return true;
      }
      return j.company.includes(target.company) || target.company.includes(j.company);
    }) ?? null
  );
}

// ---------------------------------------------------------------------------
// DOM 解析（薄封装，最佳 effort）
// ---------------------------------------------------------------------------

function textOf(el: Element | null): string {
  return (el?.textContent ?? '').trim();
}

/** DOM：解析单个职位卡片；无效卡片（无 job_detail 链接）返回 null */
export function parseJobCard(card: Element): ScannedJob | null {
  const link = card.querySelector('a[href*="/job_detail/"]') as HTMLAnchorElement | null;
  const href = link?.href ?? '';
  const title = textOf(card.querySelector('.job-name, [class*="jobName"], a[href*="/job_detail/"]'));
  const company = textOf(card.querySelector('.company-name, [class*="companyName"]'));
  const salary = textOf(card.querySelector('.salary, [class*="salary"]'));
  // jd_text：卡片描述区优先，退化为整卡可见文本（截断）
  const descEl = card.querySelector('[class*="info-desc"], .job-card-right .info-public, [class*="JobDesc"]');
  const jdText = descEl ? textOf(descEl) : textOf(card);
  return toScannedJob(href, title, company, salary, jdText);
}

/** DOM：解析卡片容器内全部职位卡片 */
export function parseJobCards(container: Element): ScannedJob[] {
  const cards = Array.from(container.querySelectorAll('li, [class*="card"], [class*="Card"]'));
  const out: ScannedJob[] = [];
  for (const c of cards) {
    const entry = parseJobCard(c);
    if (entry) {
      out.push(entry);
    }
  }
  return out;
}
