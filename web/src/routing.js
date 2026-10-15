export const homePages=['overview','devices','incidents','files'];

// Explicit fragments (including unknown ones for the 404 page) take priority.
export function resolvePage(fragment,homePage){
 return fragment || (homePages.includes(homePage)?homePage:'overview');
}
