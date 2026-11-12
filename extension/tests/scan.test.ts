import { describe, expect, it } from 'vitest';
import { toScannedJob, buildJobsList, matchJob } from '../src/content/scan';

const job = (overrides: Partial<ReturnType<typeof toScannedJob> & NonNullable<unknown>> = {}) => {
  const base = toScannedJob(
    'https://www.zhipin.com/job_detail/GolangDev01.html?lid=x',
    'Go 后端工程师',
    '示例科技',
    '25-40K·16薪',
    '负责平台后端服务开发，K8s 经验优先。',
  );
  expect(base).not.toBeNull();
  return { ...base!, ...overrides };
};

describe('扫描结果结构化（toScannedJob / buildJobsList）', () => {
  it('job_key 从职位详情 URL 提取，字段 trim', () => {
    const j = toScannedJob(
      'https://www.zhipin.com/job_detail/GolangDev01.html?lid=x',
      '  Go 工程师  ',
      ' 示例科技 ',
      ' 25-40K ',
      '  JD 文本 ',
    );
    expect(j).toEqual({
      job_key: 'GolangDev01',
      title: 'Go 工程师',
      company: '示例科技',
      salary: '25-40K',
      jd_text: 'JD 文本',
    });
  });

  it('无 job_detail 链接的卡片视为无效（null）', () => {
    expect(toScannedJob('https://www.zhipin.com/web/geek/job', 't', 'c', 's', 'j')).toBeNull();
    expect(toScannedJob('', 't', 'c', 's', 'j')).toBeNull();
  });

  it('jd_text 截断到 500 字符（最佳 effort，可为空）', () => {
    const j = toScannedJob('https://www.zhipin.com/job_detail/K1.html', 't', 'c', 's', 'x'.repeat(600));
    expect(j?.jd_text.length).toBe(500);
    const empty = toScannedJob('https://www.zhipin.com/job_detail/K2.html', 't', 'c', 's', '');
    expect(empty?.jd_text).toBe('');
  });

  it('buildJobsList 按 job_key 去重保留首个', () => {
    const a = job();
    const dup = job({ title: '重复条目' });
    const b = job({ job_key: 'Other02', title: '另一个' });
    const payload = buildJobsList('golang', [a, dup, b]);
    expect(payload.keyword).toBe('golang');
    expect(payload.jobs).toHaveLength(2);
    expect(payload.jobs[0]?.title).toBe('Go 后端工程师');
    expect(payload.jobs[1]?.job_key).toBe('Other02');
  });
});

describe('deliver.next 卡片定位（matchJob：job_key 优先，job_title 兜底）', () => {
  const jobs = [
    job({ job_key: 'AAA', title: 'Go 后端工程师', company: '示例科技' }),
    job({ job_key: 'BBB', title: '资深 Go 工程师', company: '另一家网络' }),
  ];

  it('job_key 精确匹配优先命中', () => {
    expect(matchJob(jobs, { job_key: 'BBB', job_title: 'Go 后端工程师' })?.job_key).toBe('BBB');
  });

  it('job_key 未命中时 job_title 兜底（标题互相包含）', () => {
    expect(matchJob(jobs, { job_key: 'Gone', job_title: '资深 Go 工程师' })?.job_key).toBe('BBB');
    expect(matchJob(jobs, { job_title: 'Go' })?.job_key).toBe('AAA'); // 首个包含命中
  });

  it('带 company 时要求公司也一致', () => {
    expect(matchJob(jobs, { job_title: 'Go 后端工程师', company: '另一家网络' })).toBeNull();
    expect(matchJob(jobs, { job_title: 'Go 后端工程师', company: '示例' })?.job_key).toBe('AAA');
  });

  it('无 job_key 且无 job_title 不命中；全不匹配返回 null', () => {
    expect(matchJob(jobs, {})).toBeNull();
    expect(matchJob(jobs, { job_key: 'ZZZ', job_title: 'Java 工程师' })).toBeNull();
  });
});
