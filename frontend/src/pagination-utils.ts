export function pageNumbers(page: number, totalPages: number): (number | 'start-gap' | 'end-gap')[] {
 if (totalPages <= 7) return Array.from({ length: totalPages }, (_, i) => i + 1)
 if (page <= 4) return [1, 2, 3, 4, 5, 'end-gap', totalPages]
 if (page >= totalPages - 3) return [1, 'start-gap', totalPages - 4, totalPages - 3, totalPages - 2, totalPages - 1, totalPages]
 return [1, 'start-gap', page - 1, page, page + 1, 'end-gap', totalPages]
}

export function parsePageJump(value: string, totalPages: number): number | null {
 const text = value.trim()
 if (!/^\d+$/.test(text)) return null
 const page = Number(text)
 return Number.isSafeInteger(page) && page >= 1 && page <= totalPages ? page : null
}
