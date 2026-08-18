import { z } from 'zod'

const responseSchema = z.object({
  statusCode: z
    .number({ message: 'HTTP status code is required' })
    .int('HTTP status code must be an integer')
    .min(400, 'HTTP status code must be between 400 and 599')
    .max(599, 'HTTP status code must be between 400 and 599'),
  errorMessage: z
    .string()
    .trim()
    .min(1, 'Error message is required')
    .max(512, 'Error message must be at most 512 characters'),
})

export const userRateLimitConfigSchema = z
  .object({
    delaySeconds: z
      .number({ message: 'Delay is required' })
      .int('Delay must be an integer')
      .min(0, 'Delay must be between 0 and 60 seconds')
      .max(60, 'Delay must be between 0 and 60 seconds'),
    defaultResponse: responseSchema,
    groupResponses: z.array(
      responseSchema.extend({
        group: z
          .string()
          .trim()
          .min(1, 'Group is required')
          .max(64, 'Group must be at most 64 characters'),
      })
    ),
  })
  .superRefine((value, context) => {
    const groups = new Set<string>()
    value.groupResponses.forEach((item, index) => {
      const group = item.group.trim()
      if (groups.has(group)) {
        context.addIssue({
          code: 'custom',
          message: 'Each group can only have one response override',
          path: ['groupResponses', index, 'group'],
        })
      }
      groups.add(group)
    })
  })

export type UserRateLimitConfigForm = z.infer<typeof userRateLimitConfigSchema>

export const userRateLimitRuleSchema = z
  .object({
    userId: z.number().int().positive('Select a target user'),
    group: z
      .string()
      .trim()
      .min(1, 'Group is required')
      .max(64, 'Group must be at most 64 characters'),
    totalCount: z
      .number({ message: 'Total request limit is required' })
      .int('Total request limit must be an integer')
      .min(0, 'Total request limit must be between 0 and 100000000')
      .max(100000000, 'Total request limit must be between 0 and 100000000'),
    successCount: z
      .number({ message: 'Successful request limit is required' })
      .int('Successful request limit must be an integer')
      .min(1, 'Successful request limit must be between 1 and 100000000')
      .max(
        100000000,
        'Successful request limit must be between 1 and 100000000'
      ),
    hasResponseOverride: z.boolean(),
    statusCode: z.number().int(),
    errorMessage: z.string(),
  })
  .superRefine((value, context) => {
    if (!value.hasResponseOverride) return
    const result = responseSchema.safeParse({
      statusCode: value.statusCode,
      errorMessage: value.errorMessage,
    })
    if (result.success) return
    result.error.issues.forEach((issue) => {
      const field =
        issue.path[0] === 'statusCode' ? 'statusCode' : 'errorMessage'
      context.addIssue({ ...issue, path: [field] })
    })
  })

export type UserRateLimitRuleForm = z.infer<typeof userRateLimitRuleSchema>
